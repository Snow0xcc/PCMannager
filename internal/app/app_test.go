//go:build !windows

package app

import (
	"errors"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// errFakeStart is what Start failure tests return, so assertions can line up
// with the reason surfaced through ModuleError.
var errFakeStart = errors.New("fake: 启动失败")

// fakeModule is a minimal core.Module for tests: it records the calls it
// receives so assertions can observe start/stop/option ordering.
type fakeModule struct {
	core.Base
	id string

	starts   int
	stops    int
	inits    int
	hotkeys  int
	applied  map[string]any
	startErr error

	opts []core.Option
}

func newFakeModule(id string, opts ...core.Option) *fakeModule {
	return &fakeModule{id: id, applied: map[string]any{}, opts: opts}
}

func (f *fakeModule) ID() string          { return f.id }
func (f *fakeModule) Name() string        { return "fake " + f.id }
func (f *fakeModule) Description() string { return "test double" }

func (f *fakeModule) Init(*core.Context) error { f.inits++; return nil }
func (f *fakeModule) Start() error {
	f.starts++
	return f.startErr
}
func (f *fakeModule) Stop() error            { f.stops++; return nil }
func (f *fakeModule) Options() []core.Option { return f.opts }
func (f *fakeModule) ApplyOption(key string, value any) error {
	f.applied[key] = value
	return nil
}
func (f *fakeModule) OnHotkey() error { f.hotkeys++; return nil }

// newTestApp builds an App rooted at a temp directory so no test touches the
// real user data directory (%APPDATA%\GoBox etc.).
//
// t.Chdir makes internal/app probe config.yaml from that directory, which is
// what keeps each test isolated.
func newTestApp(t *testing.T) *App {
	t.Helper()
	t.Chdir(t.TempDir())
	a, err := New()
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	t.Cleanup(func() { a.Shutdown() })
	return a
}

func TestRegisterRejectsDuplicateID(t *testing.T) {
	a := newTestApp(t)

	if err := a.Register(newFakeModule("dup")); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	if err := a.Register(newFakeModule("dup")); err == nil {
		t.Fatal("重复模块 ID 应被拒绝")
	}
}

func TestRegisterDeclaresOptionDefaults(t *testing.T) {
	a := newTestApp(t)
	mod := newFakeModule("moded", core.Option{Key: "k", Kind: core.KindInt, Default: 7})
	if err := a.Register(mod); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// The default must reach the config so the panel can echo it back and a
	// missing file still yields the declared value.
	if got := a.Config().Module("moded").Get("k", nil); got != 7 {
		t.Errorf("option 默认值 = %v, 期望 7", got)
	}
}

func TestEnableDisableRoundTrip(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("rt")
	a.MustRegister(m)
	a.InitModules()

	if err := a.EnableModule("rt", true); err != nil {
		t.Fatalf("EnableModule: %v", err)
	}
	if !a.Running("rt") {
		t.Fatal("Enable 后应处于 running")
	}
	if m.starts != 1 {
		t.Errorf("Start 调用 %d 次, 期望 1", m.starts)
	}

	// EnableModule is the real-world path (it persists the switch first), and
	// its guard is "enabled AND running": a second call must not start twice.
	if err := a.EnableModule("rt", true); err != nil {
		t.Fatalf("重复 EnableModule: %v", err)
	}
	if m.starts != 1 {
		t.Errorf("重复 EnableModule 后 Start 调用 %d 次, 期望仍为 1", m.starts)
	}

	if err := a.EnableModule("rt", false); err != nil {
		t.Fatalf("EnableModule(false): %v", err)
	}
	if a.Running("rt") {
		t.Fatal("Disable 后不应 running")
	}
	if m.stops != 1 {
		t.Errorf("Stop 调用 %d 次, 期望 1", m.stops)
	}
}

func TestEnableUnknownModule(t *testing.T) {
	a := newTestApp(t)
	if err := a.Enable("nope"); err == nil {
		t.Fatal("启用不存在的模块应报错")
	}
}

func TestStartFailureIsRecordedAsLastError(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("broken")
	m.startErr = errFakeStart
	a.MustRegister(m)

	if err := a.Enable("broken"); err == nil {
		t.Fatal("启动失败应返回错误")
	}
	if a.Running("broken") {
		t.Fatal("启动失败的模块不应标记为 running")
	}
	// The panel must be able to say *why* without opening the log file.
	if got := a.ModuleError("broken"); got == "" {
		t.Error("启动失败后 ModuleError 不应为空")
	}
}

func TestSuccessfulStartClearsLastError(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("flaky")
	m.startErr = errFakeStart
	a.MustRegister(m)
	_ = a.Enable("flaky")
	if a.ModuleError("flaky") == "" {
		t.Fatal("前置条件：失败后应有错误信息")
	}

	m.startErr = nil
	if err := a.Enable("flaky"); err != nil {
		t.Fatalf("重试 Enable: %v", err)
	}
	if got := a.ModuleError("flaky"); got != "" {
		t.Errorf("成功启动后 ModuleError = %q, 期望空", got)
	}
}

func TestApplyOptionRestartsWhenOptionRequiresIt(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("restarty",
		core.Option{Key: "layout", Kind: core.KindString, Default: "a", Restart: true},
		core.Option{Key: "soft", Kind: core.KindInt, Default: 1},
	)
	a.MustRegister(m)
	if err := a.Enable("restarty"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	startsBefore := m.starts

	// Restart-marked options must re-run Start, otherwise the new value never
	// reaches a window/layout built at startup.
	if err := a.ApplyOption("restarty", "layout", "b"); err != nil {
		t.Fatalf("ApplyOption: %v", err)
	}
	if m.starts != startsBefore+1 {
		t.Errorf("Restart 选项变更后 Start 次数 = %d, 期望 %d", m.starts, startsBefore+1)
	}

	// Non-restart options must NOT restart: doing so would drop live state.
	startsBefore = m.starts
	if err := a.ApplyOption("restarty", "soft", 5); err != nil {
		t.Fatalf("ApplyOption(soft): %v", err)
	}
	if m.starts != startsBefore {
		t.Errorf("非 Restart 选项 Start 次数变化了: %d -> %d", startsBefore, m.starts)
	}
	if m.applied["soft"] != 5 {
		t.Errorf("ApplyOption 未传递给模块, applied = %v", m.applied)
	}
}

func TestApplyOptionUnknownModule(t *testing.T) {
	a := newTestApp(t)
	if err := a.ApplyOption("ghost", "k", 1); err == nil {
		t.Fatal("对不存在的模块应用配置应报错")
	}
}

func TestSetModuleSettingsRejectsInvalidHotkey(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(newFakeModule("hk"))

	bad := "not+a+real+hotkey"
	if err := a.SetModuleSettings("hk", nil, &bad, nil); err == nil {
		t.Fatal("无效热键应被拒绝")
	}
	// A rejected write must not reach the config file.
	if got := a.Config().Module("hk").Hotkey(); got == bad {
		t.Error("被拒绝的热键不应写入配置")
	}
}

func TestBindHotkeyRecordsInvalidReason(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("hkbad")
	a.MustRegister(m)
	if err := a.Config().Module("hkbad").SetHotkey("ctrl+alt+nosuchkey"); err != nil {
		t.Fatalf("SetHotkey: %v", err)
	}

	a.bindHotkey(m)
	// Off Windows registration is unsupported, but the *reason* must still be
	// attributable: a dead shortcut must explain itself in the panel.
	if got := a.ModuleError("hkbad"); got == "" {
		t.Error("无效/不可用的热键应记录原因")
	}
}

func TestVersionNeverBlank(t *testing.T) {
	if Version == "" {
		t.Fatal("Version 不应为空：面板与 /api/state 都依赖它")
	}
}

func TestSetModuleSettingsAppliesEnabledAndOptions(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("both", core.Option{Key: "n", Kind: core.KindInt, Default: 1})
	a.MustRegister(m)

	on := true
	if err := a.SetModuleSettings("both", &on, nil, map[string]any{"n": 42}); err != nil {
		t.Fatalf("SetModuleSettings: %v", err)
	}
	if !a.Running("both") {
		t.Error("enabled=true 应启动模块")
	}
	if m.applied["n"] != 42 {
		t.Errorf("option 未应用, applied = %v", m.applied)
	}

	off := false
	if err := a.SetModuleSettings("both", &off, nil, nil); err != nil {
		t.Fatalf("SetModuleSettings(off): %v", err)
	}
	if a.Running("both") {
		t.Error("enabled=false 应停止模块")
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	a := newTestApp(t)
	m := newFakeModule("sd")
	a.MustRegister(m)
	if err := a.Enable("sd"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	a.Shutdown()
	stopsAfterFirst := m.stops
	a.Shutdown() // must not panic, double-stop, or re-close the bus
	if m.stops != stopsAfterFirst {
		t.Errorf("重复 Shutdown 又 Stop 了模块: %d -> %d", stopsAfterFirst, m.stops)
	}
}

func TestRunHotkeyAndOpenUIUnknownModule(t *testing.T) {
	a := newTestApp(t)
	if err := a.RunHotkey("ghost"); err == nil {
		t.Error("RunHotkey 未知模块应报错")
	}
	if err := a.OpenUI("ghost"); err == nil {
		t.Error("OpenUI 未知模块应报错")
	}
}

func TestModuleErrorEmptyForHealthyModule(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(newFakeModule("fine"))
	if err := a.Enable("fine"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if got := a.ModuleError("fine"); got != "" {
		t.Errorf("健康模块 ModuleError = %q, 期望空", got)
	}
}

func TestModulesAreInRegistrationOrder(t *testing.T) {
	a := newTestApp(t)
	for _, id := range []string{"c", "a", "b"} {
		a.MustRegister(newFakeModule(id))
	}
	got := a.Modules()
	if len(got) != 3 {
		t.Fatalf("模块数 = %d, 期望 3", len(got))
	}
	// Declaration order (not sorted): the panel renders in this order.
	want := []string{"c", "a", "b"}
	for i, id := range want {
		if got[i].ID() != id {
			t.Errorf("顺序 [%d] = %q, 期望 %q", i, got[i].ID(), id)
		}
	}
}

func TestHotkeyConflictReportedNotFatal(t *testing.T) {
	a := newTestApp(t)
	x := newFakeModule("x")
	y := newFakeModule("y")
	a.MustRegister(x)
	a.MustRegister(y)

	if err := a.Config().Module("x").SetHotkey("ctrl+alt+k"); err != nil {
		t.Fatalf("SetHotkey x: %v", err)
	}
	if err := a.Config().Module("y").SetHotkey("ctrl+alt+k"); err != nil {
		t.Fatalf("SetHotkey y: %v", err)
	}

	// Off Windows the backend refuses every registration, so the combo never
	// lands in HotkeyManager.combos and Conflicts() (which only inspects
	// successfully-held combos) stays empty by design. What must still hold:
	// binding the same combo twice neither panics nor aborts the run, and each
	// module records *a* reason for its dead shortcut.
	a.RebindHotkeys()

	if got := a.ModuleError("x"); got == "" {
		t.Error("注册失败的热键应记录原因")
	}
	if got := a.ModuleError("y"); got == "" {
		t.Error("重复热键的第二个模块也应记录原因")
	}
}

// Note: HotkeyManager.Conflicts is exercised in internal/core with an
// always-succeeding fake backend. Here the real backend refuses every
// registration off Windows, so a conflict can never be observed and asserting
// on it would encode a platform accident rather than intended behaviour.
