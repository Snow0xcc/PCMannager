// Package server (see also provider.go) implements the HTTP layer that backs
// the preferences panel: a small JSON REST API, a Server-Sent Events stream
// that carries the module event bus, and the embedded single-page frontend.
//
// Everything here is platform-neutral and cgo-free, so the panel works on
// every target GoBox builds for.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configures the HTTP server.
type Options struct {
	// Port is the TCP port to listen on. 0 lets the OS choose a free port,
	// which is the default so a busy default port never blocks startup.
	Port int
	// Host is the bind address; it stays loopback so the panel is local-only.
	Host string
	// Log receives server-side diagnostics.
	Log *slog.Logger
}

// Server serves the preferences panel API.
type Server struct {
	provider Provider
	log      *slog.Logger
	srv      *http.Server
	mu       sync.Mutex
	url      string
}

// New wires the routes but does not listen; call Start.
func New(p Provider, opts Options) *Server {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	s := &Server{provider: p, log: opts.Log}
	mux := http.NewServeMux()

	// Panel shell.
	mux.HandleFunc("GET /", s.handleIndex)

	// REST API.
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/app", s.handleGetApp)
	mux.HandleFunc("PATCH /api/app", s.handlePatchApp)
	mux.HandleFunc("GET /api/modules", s.handleListModules)
	mux.HandleFunc("GET /api/modules/{id}", s.handleGetModule)
	mux.HandleFunc("PATCH /api/modules/{id}", s.handlePatchModule)
	mux.HandleFunc("POST /api/modules/{id}/actions/{action}", s.handleRunAction)
	mux.HandleFunc("POST /api/modules/{id}/hotkey", s.handleRunHotkey)
	mux.HandleFunc("POST /api/modules/{id}/ui", s.handleOpenUI)
	mux.HandleFunc("POST /api/hotkey/validate", s.handleValidateHotkey)

	// Live event stream.
	mux.HandleFunc("GET /api/events", s.handleEvents)

	s.srv = &http.Server{
		Addr:              net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Start binds the listener in the background and returns the panel URL.
//
// A failure to listen is reported but never fatal: GoBox must keep running
// (tray + hotkeys) even when the panel cannot be served.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return "", fmt.Errorf("监听面板端口失败: %w", err)
	}

	addr := ln.Addr().(*net.TCPAddr)
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port))

	s.mu.Lock()
	s.url = url
	s.mu.Unlock()

	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logf("面板服务异常退出: %v", err)
		}
	}()
	return url, nil
}

// URL returns the panel address, or "" before Start succeeds.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

// Shutdown stops the HTTP server, waiting up to the context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// logf is a nil-safe logger.
func (s *Server) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Warn(fmt.Sprintf(format, args...))
	}
}

// writeJSON serialises v with a compact envelope.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr reports an error as JSON.
func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// decodeBody parses a JSON request body into v, rejecting unknown fields so
// typos in the frontend surface as errors instead of silent no-ops.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("请求体解析失败: %w", err))
		return false
	}
	return true
}

// handleIndex serves the embedded single-page panel.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := frontend.ReadFile("web/index.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("面板资源缺失: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// handleState returns everything the panel needs in one round trip.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":      s.provider.Version(),
		"app":          s.provider.AppConfig(),
		"modules":      s.provider.Modules(),
		"capabilities": s.provider.Capabilities(),
	})
}

// handleGetApp returns the application settings.
func (s *Server) handleGetApp(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.provider.AppConfig())
}

// handlePatchApp applies an incremental settings change.
func (s *Server) handlePatchApp(w http.ResponseWriter, r *http.Request) {
	var patch AppConfigPatch
	if !decodeBody(w, r, &patch) {
		return
	}
	if err := s.provider.PatchAppConfig(patch); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.provider.AppConfig())
}

// handleListModules returns every registered module.
func (s *Server) handleListModules(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.provider.Modules())
}

// handleGetModule returns one module snapshot.
func (s *Server) handleGetModule(w http.ResponseWriter, r *http.Request) {
	info, ok := s.provider.Module(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("未找到模块 %s", r.PathValue("id")))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handlePatchModule applies enabled/hotkey/option changes.
func (s *Server) handlePatchModule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch ModulePatch
	if !decodeBody(w, r, &patch) {
		return
	}
	if err := s.provider.PatchModule(id, patch); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	info, ok := s.provider.Module(id)
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("未找到模块 %s", id))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleRunAction triggers a declared module action.
func (s *Server) handleRunAction(w http.ResponseWriter, r *http.Request) {
	module, action := r.PathValue("id"), r.PathValue("action")

	// Params arrive as a flat object; actions want map[string]string.
	raw := map[string]string{}
	if r.ContentLength > 0 {
		if !decodeBody(w, r, &raw) {
			return
		}
	} else if q := r.URL.Query(); len(q) > 0 {
		for k, v := range q {
			raw[k] = strings.Join(v, ",")
		}
	}

	if err := s.provider.RunAction(module, action, raw); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRunHotkey invokes a module's hotkey handler.
func (s *Server) handleRunHotkey(w http.ResponseWriter, r *http.Request) {
	if err := s.provider.RunHotkey(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOpenUI opens a module's native window.
func (s *Server) handleOpenUI(w http.ResponseWriter, r *http.Request) {
	if err := s.provider.OpenUI(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleValidateHotkey checks a hotkey string for the panel's inline editor.
func (s *Server) handleValidateHotkey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hotkey string `json:"hotkey"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.provider.ValidateHotkey(body.Hotkey); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleEvents streams the module event bus as Server-Sent Events.
//
// The browser reconnects on its own, so a dropped connection simply unsubscribes.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("当前连接不支持流式响应"))
		return
	}

	ch, unsubscribe := s.provider.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	// Heartbeat keeps proxies from closing an idle stream.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			if _, err := w.Write([]byte("event: " + ev.Type + "\ndata: ")); err != nil {
				return
			}
			if err := enc.Encode(ev); err != nil {
				return
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
