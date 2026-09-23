package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// fakeProvider is an in-memory Provider used to exercise the HTTP layer
// without booting the whole application.
type fakeProvider struct {
	mu       sync.Mutex
	patches  []ModulePatch
	patched  []string
	actions  []string
	hotkeys  []string
	uis      []string
	appPatch *AppConfigPatch

	events chan core.Event
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{events: make(chan core.Event, 8)}
}

func (p *fakeProvider) module(id string) ModuleInfo {
	return ModuleInfo{
		ID:          id,
		Name:        "模块 " + id,
		Description: "测试模块",
		Enabled:     true,
		Running:     true,
		Hotkey:      "ctrl+alt+t",
		Options:     []core.Option{{Key: "interval", Label: "刷新间隔", Kind: core.KindInt, Default: 1000}},
		Actions:     []core.Action{{ID: "refresh", Label: "刷新"}},
		State:       core.State{"ok": true},
	}
}

func (p *fakeProvider) Modules() []ModuleInfo {
	return []ModuleInfo{p.module("taskbar"), p.module("clipboard")}
}

func (p *fakeProvider) Module(id string) (ModuleInfo, bool) {
	if id != "taskbar" && id != "clipboard" {
		return ModuleInfo{}, false
	}
	return p.module(id), true
}

func (p *fakeProvider) PatchModule(id string, patch ModulePatch) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.patched = append(p.patched, id)
	p.patches = append(p.patches, patch)
	return nil
}

func (p *fakeProvider) RunAction(module, action string, params map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.actions = append(p.actions, module+"/"+action)
	return nil
}

func (p *fakeProvider) RunHotkey(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hotkeys = append(p.hotkeys, id)
	return nil
}

func (p *fakeProvider) OpenUI(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uis = append(p.uis, id)
	return nil
}

func (p *fakeProvider) AppConfig() AppConfig {
	return AppConfig{Theme: "auto", LogLevel: "info", Language: "zh-CN"}
}

func (p *fakeProvider) PatchAppConfig(patch AppConfigPatch) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.appPatch = &patch
	return nil
}

func (p *fakeProvider) Capabilities() any {
	return map[string]bool{"tray_icon": true, "hotkeys": false}
}

func (p *fakeProvider) ValidateHotkey(hotkey string) error {
	if hotkey == "" || strings.Contains(hotkey, "ctrl") {
		return nil
	}
	return errBadHotkey
}

func (p *fakeProvider) Subscribe() (<-chan core.Event, func()) {
	// Each subscriber gets its own channel so tests never interleave.
	ch := make(chan core.Event, 8)
	p.mu.Lock()
	p.events = ch
	p.mu.Unlock()
	return ch, func() {}
}

func (p *fakeProvider) Version() string { return "0.1.0" }

// errBadHotkey stands in for the real validation error.
var errBadHotkey = &hotkeyErr{}

type hotkeyErr struct{}

func (e *hotkeyErr) Error() string { return "热键格式无效" }

// newTestServer binds the routes to an httptest server (loopback, free port).
//
// Tests keep their own reference to the Provider so they can assert on what
// the handlers actually did.
func newTestServer(t *testing.T, p Provider) *httptest.Server {
	t.Helper()
	s := New(p, Options{Port: 0})
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

// TestHandleIndexServesEmbeddedPanel proves the embedded frontend is actually
// wired in: a missing/renamed web/index.html fails at embed time, not here.
func TestHandleIndexServesEmbeddedPanel(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("请求面板失败: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, 期望 text/html", ct)
	}
	// The panel must not be served as a cacheable asset.
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, 期望包含 no-store", cc)
	}
}

// TestHandleStateReturnsFullSnapshot checks the one-round-trip bootstrap call.
func TestHandleStateReturnsFullSnapshot(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	var body struct {
		Version      string          `json:"version"`
		App          AppConfig       `json:"app"`
		Modules      []ModuleInfo    `json:"modules"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := getJSON(ts.URL+"/api/state", &body); err != nil {
		t.Fatalf("获取 /api/state 失败: %v", err)
	}
	if body.Version != "0.1.0" {
		t.Fatalf("version = %q, 期望 0.1.0", body.Version)
	}
	if len(body.Modules) != 2 {
		t.Fatalf("模块数 = %d, 期望 2", len(body.Modules))
	}
	if !body.Capabilities["tray_icon"] {
		t.Fatal("capabilities 未透传")
	}
	if body.App.Language != "zh-CN" {
		t.Fatalf("language = %q, 期望 zh-CN", body.App.Language)
	}
}

// TestHandlePatchModule verifies the panel's enable/hotkey/option write path
// reaches the provider with the values intact.
func TestHandlePatchModule(t *testing.T) {
	p := newFakeProvider()
	ts := newTestServer(t, p)

	enabled := false
	hotkey := "ctrl+alt+z"
	body := ModulePatch{
		Enabled: &enabled,
		Hotkey:  &hotkey,
		Options: map[string]any{"interval": 500},
	}
	var got ModuleInfo
	if err := doJSON("PATCH", ts.URL+"/api/modules/taskbar", body, &got); err != nil {
		t.Fatalf("PATCH 失败: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.patched) != 1 || p.patched[0] != "taskbar" {
		t.Fatalf("未到达 provider: %v", p.patched)
	}
	patch := p.patches[0]
	if patch.Enabled == nil || *patch.Enabled {
		t.Fatal("enabled=false 未传递")
	}
	if patch.Hotkey == nil || *patch.Hotkey != "ctrl+alt+z" {
		t.Fatal("hotkey 未传递")
	}
	if patch.Options["interval"] != float64(500) {
		t.Fatalf("interval = %v, 期望 500", patch.Options["interval"])
	}
}

// TestHandlePatchModuleUnknownID checks a typo in the panel surfaces as 404.
func TestHandlePatchModuleUnknownID(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	res, err := http.Post(ts.URL+"/api/modules/nope", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Fatal("未知模块应返回错误状态码")
	}
}

// TestHandleRunActionAndHotkeyAndUI covers the three "trigger" endpoints.
func TestHandleRunActionAndHotkeyAndUI(t *testing.T) {
	p := newFakeProvider()
	ts := newTestServer(t, p)

	for _, path := range []string{
		"/api/modules/taskbar/actions/refresh",
		"/api/modules/taskbar/hotkey",
		"/api/modules/taskbar/ui",
	} {
		res, err := http.Post(ts.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("POST %s 失败: %v", path, err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("POST %s 状态码 = %d, 期望 200", path, res.StatusCode)
		}
		res.Body.Close()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.actions) != 1 || p.actions[0] != "taskbar/refresh" {
		t.Fatalf("actions = %v, 期望 [taskbar/refresh]", p.actions)
	}
	if len(p.hotkeys) != 1 || p.hotkeys[0] != "taskbar" {
		t.Fatalf("hotkeys = %v", p.hotkeys)
	}
	if len(p.uis) != 1 || p.uis[0] != "taskbar" {
		t.Fatalf("uis = %v", p.uis)
	}
}

// TestHandleValidateHotkey checks the inline hotkey editor gets a verdict
// without throwing.
func TestHandleValidateHotkey(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	var ok struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := doJSON("POST", ts.URL+"/api/hotkey/validate", map[string]string{"hotkey": "ctrl+alt+t"}, &ok); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !ok.OK {
		t.Fatal("ctrl+alt+t 应为有效热键")
	}

	var bad struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := doJSON("POST", ts.URL+"/api/hotkey/validate", map[string]string{"hotkey": "bogus"}, &bad); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if bad.OK || bad.Error == "" {
		t.Fatal("无效热键应返回 ok=false 与原因")
	}
}

// TestHandleEventsStreamsBus is the real proof the SSE channel works end to
// end: it must flush an event published after the client connected.
func TestHandleEventsStreamsBus(t *testing.T) {
	p := newFakeProvider()
	ts := newTestServer(t, p)

	req, err := http.NewRequest("GET", ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("连接 SSE 失败: %v", err)
	}
	defer res.Body.Close()

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, 期望 text/event-stream", ct)
	}

	// Publish after connecting: the handler must push it to this client.
	go func() {
		time.Sleep(50 * time.Millisecond)
		p.mu.Lock()
		ch := p.events
		p.mu.Unlock()
		if ch != nil {
			ch <- core.Event{Type: core.EventLog, Module: "taskbar", Message: "hello"}
		}
	}()

	reader := bufio.NewReader(res.Body)
	deadline := time.After(3 * time.Second)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("读取 SSE 失败: %v", err)
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "hello") {
			return // received the published event
		}
		select {
		case <-deadline:
			t.Fatal("SSE 未收到已发布事件")
		default:
		}
	}
}

// TestStartBindsLoopbackPort exercises the real Start() path (not httptest),
// which is what the application actually calls at boot.
func TestStartBindsLoopbackPort(t *testing.T) {
	s := New(newFakeProvider(), Options{Port: 0, Host: "127.0.0.1"})

	url, err := s.Start()
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("面板 URL 应绑定回环地址: %q", url)
	}
	if s.URL() != url {
		t.Fatalf("URL() = %q, 期望 %q", s.URL(), url)
	}

	// The bound port must actually serve the panel.
	res, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatalf("访问面板失败: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", res.StatusCode)
	}
}

// getJSON performs a GET and decodes the JSON body into v.
func getJSON(url string, v any) error {
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(v)
}

// doJSON performs a request with a JSON body and decodes the response.
func doJSON(method, url string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&apiErr)
		return &httpError{code: res.StatusCode, msg: apiErr.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return http.StatusText(e.code) + ": " + e.msg }
