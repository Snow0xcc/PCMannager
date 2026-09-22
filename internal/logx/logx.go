// Package logx configures the shared structured logger and tees records into
// the event bus so the preferences panel can show live logs (PRD GFR-8).
package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BusSink receives every log record for fan-out to the panel.
type BusSink interface {
	Log(module, level, msg string)
}

// Options configures logger construction.
type Options struct {
	// Level is one of debug/info/warn/error (defaults to info).
	Level string
	// File is the log file path; empty disables file logging.
	File string
	// Sink, when non-nil, receives records for the preferences panel.
	Sink BusSink
	// Console mirrors records to stderr (useful when run from a terminal).
	Console bool
}

// ParseLevel converts a config string into an slog.Level.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// logger wraps a slog.Logger with the module attribution used by the panel.
type logger struct {
	*slog.Logger
	module string
}

// New builds the root logger described by opts.
//
// File logging never fails the application: if the log file cannot be opened
// the logger degrades to console/bus-only and reports the problem once.
func New(opts Options) (*slog.Logger, io.Closer, error) {
	var (
		writers []io.Writer
		closer  io.Closer
	)

	if opts.Console {
		writers = append(writers, os.Stderr)
	}
	if opts.File != "" {
		if err := os.MkdirAll(filepath.Dir(opts.File), 0o755); err != nil {
			writers = append(writers, os.Stderr)
		} else if f, err := os.OpenFile(opts.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			writers = append(writers, f)
			closer = f
		}
	}

	// A very small log window keeps the panel honest without unbounded memory.
	handlers := make([]slog.Handler, 0, 2)
	if len(writers) > 0 {
		out := io.Discard
		if len(writers) == 1 {
			out = writers[0]
		} else {
			out = io.MultiWriter(writers...)
		}
		handlers = append(handlers, slog.NewTextHandler(out, &slog.HandlerOptions{
			Level: ParseLevel(opts.Level),
		}))
	} else {
		handlers = append(handlers, slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Sink != nil {
		handlers = append(handlers, &busHandler{sink: opts.Sink, level: ParseLevel(opts.Level), attrs: map[string]string{}})
	}

	return slog.New(&fanout{handlers: handlers}), closer, nil
}

// fanout duplicates each record to every handler.
type fanout struct {
	handlers []slog.Handler
}

func (f *fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f *fanout) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (f *fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &fanout{handlers: next}
}

func (f *fanout) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &fanout{handlers: next}
}

// busHandler forwards records to the panel bus. Any "module" attribute is
// lifted out so the panel can filter logs per module.
type busHandler struct {
	mu     sync.Mutex
	sink   BusSink
	level  slog.Level
	attrs  map[string]string
	groups []string
}

func (h *busHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *busHandler) Handle(_ context.Context, r slog.Record) error {
	module := h.attrs["module"]
	msg := r.Message

	// Inline attributes into the message for a compact single-line log view.
	var extra []string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "module" {
			module = a.Value.String()
			return true
		}
		extra = append(extra, a.Key+"="+a.Value.String())
		return true
	})
	if len(extra) > 0 {
		msg += "  [" + strings.Join(extra, " ") + "]"
	}
	h.sink.Log(module, levelName(r.Level), msg)
	return nil
}

func (h *busHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	merged := make(map[string]string, len(h.attrs)+len(attrs))
	for k, v := range h.attrs {
		merged[k] = v
	}
	for _, a := range attrs {
		merged[a.Key] = a.Value.String()
	}
	return &busHandler{sink: h.sink, level: h.level, attrs: merged, groups: h.groups}
}

func (h *busHandler) WithGroup(name string) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	groups := append(append([]string{}, h.groups...), name)
	return &busHandler{sink: h.sink, level: h.level, attrs: h.attrs, groups: groups}
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// TimeFormat is the timestamp layout used in log lines and the panel.
const TimeFormat = time.RFC3339

// With returns a logger tagged with a module name for the panel.
func With(base *slog.Logger, module string) *slog.Logger {
	return base.With("module", module)
}
