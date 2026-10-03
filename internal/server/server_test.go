package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// testToken is the fixed panel token the test helpers inject; production
// generates a fresh 128-bit random value per boot (newToken + Start).
const testToken = "pcm-unit-test-token"

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
	return AppConfig{Theme: "auto", LogLevel: "info", Language: "zh-CN",
		RestartRequired: []string{"server_port", "data_dir", "log_level"}}
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
	s := New(p, Options{Port: 0, Token: testToken})
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

// mutate sends a raw mutation request with caller-controlled headers so the
// B1 auth tests can exercise missing/wrong credentials exactly as an attacker
// (or the real panel) would. It defaults to a JSON content type; passing
// "Content-Type": "" in headers removes the header entirely.
func mutate(t *testing.T, method, url string, headers map[string]string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, url, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
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

// TestHandleGetAppRestartsRequired（A7）：面板必须能得知哪些应用设置
// 只在启动时读取，否则保存 server_port 后用户无从知道为何不生效。
func TestHandleGetAppRestartsRequired(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	var body AppConfig
	if err := getJSON(ts.URL+"/api/app", &body); err != nil {
		t.Fatalf("获取 /api/app 失败: %v", err)
	}
	want := map[string]bool{"server_port": false, "data_dir": false, "log_level": false}
	for _, k := range body.RestartRequired {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("restart_required 缺少 %q", k)
		}
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

	err := doJSON("PATCH", ts.URL+"/api/modules/nope", map[string]any{"enabled": false}, nil)
	if err == nil {
		t.Fatal("未知模块应返回错误状态码")
	}
	var httpErr *httpError
	if !errors.As(err, &httpErr) {
		t.Fatalf("期望 httpError, 得到 %T: %v", err, err)
	}
	if httpErr.code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, 期望 404", httpErr.code)
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
		var ok map[string]any
		if err := doJSON("POST", ts.URL+path, map[string]any{}, &ok); err != nil {
			t.Fatalf("POST %s 失败: %v", path, err)
		}
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
	var sawEventName bool
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("读取 SSE 失败: %v", err)
		}
		// 契约（A1）：服务端发送具名事件 `event: <type>`，前端按类型
		// addEventListener（internal/panel/panel_test.go 钉住另一侧）。
		if strings.HasPrefix(line, "event: ") && strings.TrimSpace(line) == "event: "+core.EventLog {
			sawEventName = true
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "hello") {
			if !sawEventName {
				t.Fatal("SSE 帧缺少 event: 名——前端按类型监听将收不到该事件")
			}
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

	// The bound port must actually serve the panel (strip the token query
	// first: relative API calls never inherit the page URL's query).
	base := strings.SplitN(url, "?", 2)[0]
	res, err := http.Get(base + "/api/state")
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
// It injects the panel token and a JSON content type, i.e. exactly what the
// real frontend's api() wrapper sends (B1); auth-negative tests use mutate
// instead so they control the raw headers.
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
	req.Header.Set("X-PCMT-Token", testToken)
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

// --- B1 面板鉴权 / CSRF 防护（ROADMAP 阶段 B1）---
//
// server 只绑 127.0.0.1 不足以防本机任意网页经 no-cors 触发写操作，
// 以下用例按性价比顺序钉住四层闸门：token、JSON Content-Type、
// Sec-Fetch-Site、Host 回环校验；GET 读路径保持免鉴权。

// TestMutationWithoutTokenRejected（B1-1）：变更类路由缺 token / 错 token
// 一律 403，拒绝原因写入响应体，且 provider 不得被触达。
func TestMutationWithoutTokenRejected(t *testing.T) {
	p := newFakeProvider()
	ts := newTestServer(t, p)

	cases := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
	}{
		{"PATCH 缺 token", "PATCH", "/api/modules/taskbar", nil},
		{"PATCH 错 token", "PATCH", "/api/modules/taskbar", map[string]string{"X-PCMT-Token": "wrong"}},
		{"POST action 缺 token", "POST", "/api/modules/taskbar/actions/refresh", nil},
		{"POST ui query 错 token", "POST", "/api/modules/taskbar/ui?token=wrong", nil},
	}
	for _, tc := range cases {
		res := mutate(t, tc.method, ts.URL+tc.path, tc.headers, `{}`)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s: 状态码 = %d, 期望 403", tc.name, res.StatusCode)
		}
		body, _ := io.ReadAll(res.Body)
		if len(strings.TrimSpace(string(body))) == 0 {
			t.Errorf("%s: 拒绝响应体为空, 应写入拒绝原因", tc.name)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.patched)+len(p.actions)+len(p.uis) != 0 {
		t.Errorf("被拒绝的请求不应触达 provider: patched=%v actions=%v uis=%v", p.patched, p.actions, p.uis)
	}
}

// TestMutationWithTokenAccepted（B1-1 正例）：token 放头或放 query 都放行，
// 写操作完整到达 provider。
func TestMutationWithTokenAccepted(t *testing.T) {
	p := newFakeProvider()
	ts := newTestServer(t, p)

	enabled := false
	var got ModuleInfo
	if err := doJSON("PATCH", ts.URL+"/api/modules/taskbar", ModulePatch{Enabled: &enabled}, &got); err != nil {
		t.Fatalf("带 token 的 PATCH 应成功: %v", err)
	}

	// 同一 token 放 query 也必须放行（页面 URL 即此形态）。
	res := mutate(t, "POST", ts.URL+"/api/modules/taskbar/hotkey?token="+testToken, nil, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("query token 的 POST 状态码 = %d, 期望 200", res.StatusCode)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.patched) != 1 || len(p.hotkeys) != 1 {
		t.Fatalf("provider 未收到写请求: patched=%v hotkeys=%v", p.patched, p.hotkeys)
	}
}

// TestMutationRequiresJSONContentType（B1-2）：变更路由必须携带
// Content-Type: application/json。CORS 简单请求带不了它，这一条直接挡掉
// 全部 no-cors 表单/fetch 探测。
func TestMutationRequiresJSONContentType(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	res := mutate(t, "PATCH", ts.URL+"/api/modules/taskbar",
		map[string]string{"X-PCMT-Token": testToken, "Content-Type": "text/plain"}, `{}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("Content-Type=text/plain 状态码 = %d, 期望 400", res.StatusCode)
	}

	// 完全不带 Content-Type 同样拒绝。
	res = mutate(t, "POST", ts.URL+"/api/modules/taskbar/ui",
		map[string]string{"X-PCMT-Token": testToken, "Content-Type": ""}, ``)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺 Content-Type 状态码 = %d, 期望 400", res.StatusCode)
	}
}

// TestRejectsCrossSiteFetch（B1-3）：浏览器在跨站请求上标注
// Sec-Fetch-Site: cross-site，服务端直接拒绝——即使 token/CT 都合法。
func TestRejectsCrossSiteFetch(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	headers := map[string]string{"X-PCMT-Token": testToken, "Sec-Fetch-Site": "cross-site"}
	if res := mutate(t, "PATCH", ts.URL+"/api/modules/taskbar", headers, `{}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("Sec-Fetch-Site=cross-site 状态码 = %d, 期望 403", res.StatusCode)
	}

	// 同源标记（真实面板发出）不受影响。
	headers["Sec-Fetch-Site"] = "same-origin"
	if res := mutate(t, "PATCH", ts.URL+"/api/modules/taskbar", headers, `{}`); res.StatusCode != http.StatusOK {
		t.Fatalf("Sec-Fetch-Site=same-origin 状态码 = %d, 期望 200", res.StatusCode)
	}
}

// TestRejectsNonLoopbackHost（B1-4）：Host 头必须为回环地址，挡 DNS rebinding
// （攻击者把解析到 127.0.0.1 的域名指过来时，Host 会暴露真实域名）。
func TestRejectsNonLoopbackHost(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	req, err := http.NewRequest("PATCH", ts.URL+"/api/modules/taskbar", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Host = "attacker.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PCMT-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("恶意 Host 状态码 = %d, 期望 403", res.StatusCode)
	}

	// localhost:port 是合法面板地址（用户手动输入的形态），必须放行。
	u, err := neturl.Parse(ts.URL)
	if err != nil {
		t.Fatalf("解析测试地址失败: %v", err)
	}
	req2, err := http.NewRequest("GET", ts.URL+"/api/state", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req2.Host = "localhost:" + u.Port()
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("Host=localhost 状态码 = %d, 期望 200", res2.StatusCode)
	}
}

// TestReadPathStaysOpen（B1 读路径）：GET 免 token——面板首屏加载与 SSE
// 都依赖它，不能被鉴权闸门误伤。
func TestReadPathStaysOpen(t *testing.T) {
	ts := newTestServer(t, newFakeProvider())

	res, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("无 token 的 GET /api/state 状态码 = %d, 期望 200（读路径不受影响）", res.StatusCode)
	}
}

// TestStartCarriesTokenInURL：真实 Start() 路径生成 ≥128bit 随机 token 并拼进
// 面板 URL（App.PanelURL 经 SetPanelURL 透传该地址，info 日志亦记录它），
// 且该 token 真能通过变更闸门。
func TestStartCarriesTokenInURL(t *testing.T) {
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

	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("解析面板 URL 失败: %v", err)
	}
	tok := u.Query().Get("token")
	if len(tok) < 32 {
		t.Fatalf("面板 URL 应携带 ≥128bit（32 hex 字符）token: %q", url)
	}
	if tok == testToken {
		t.Fatal("Start 应独立生成 token, 而不是复用外部值")
	}

	apiBase := "http://" + u.Host
	// 读路径无 token 可用。
	res, err := http.Get(apiBase + "/api/state")
	if err != nil {
		t.Fatalf("读路径请求失败: %v", err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/state 状态码 = %d, 期望 200", res.StatusCode)
	}

	// 写路径：缺 token 拒绝。
	mkReq := func() *http.Request {
		req, _ := http.NewRequest("POST", apiBase+"/api/modules/taskbar/hotkey", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	resNoTok, err := http.DefaultClient.Do(mkReq())
	if err != nil {
		t.Fatalf("写路径请求失败: %v", err)
	}
	io.Copy(io.Discard, resNoTok.Body)
	resNoTok.Body.Close()
	if resNoTok.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 token 的 POST 状态码 = %d, 期望 403", resNoTok.StatusCode)
	}

	// 写路径：URL 里的 token 放头即可放行。
	req := mkReq()
	req.Header.Set("X-PCMT-Token", tok)
	resTok, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("写路径请求失败: %v", err)
	}
	io.Copy(io.Discard, resTok.Body)
	resTok.Body.Close()
	if resTok.StatusCode != http.StatusOK {
		t.Fatalf("带 URL token 的 POST 状态码 = %d, 期望 200", resTok.StatusCode)
	}
}
