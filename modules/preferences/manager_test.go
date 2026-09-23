//go:build !windows

package preferences

import (
	"testing"

	"github.com/snow0xcc/pcmannager/internal/app"
	"github.com/snow0xcc/pcmannager/internal/core"
)

// fakeModule is the smallest core.Module that can be registered; preferences
// only ever reads ID/Name/Options, so nothing else is exercised here.
type fakeModule struct {
	core.Base
	id   string
	name string
}

func (f *fakeModule) ID() string               { return f.id }
func (f *fakeModule) Name() string             { return f.name }
func (f *fakeModule) Description() string      { return "test double" }
func (f *fakeModule) Init(*core.Context) error { return nil }
func (f *fakeModule) Start() error             { return nil }
func (f *fakeModule) Stop() error              { return nil }

// newTestApp builds an app rooted in a temp dir so no test touches the real
// user data directory.
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	t.Chdir(t.TempDir())
	a, err := app.New()
	if err != nil {
		t.Fatalf("app.New() 失败: %v", err)
	}
	t.Cleanup(a.Shutdown)
	return a
}

func TestNewManagerExposesApp(t *testing.T) {
	a := newTestApp(t)
	m := NewManager(a)
	if m.App() != a {
		t.Fatal("App() 应返回构造时传入的 app")
	}
}

func TestManagerLogIsAvailable(t *testing.T) {
	m := NewManager(newTestApp(t))
	if m.Log() == nil {
		t.Fatal("App 就绪时 Log() 不应为 nil")
	}
}

// The methods below are written against a nil receiver so the panel code can
// call them on a Manager that failed to construct without a nil-deref panic.
// That tolerance is deliberate: Show() runs on the UI thread, where a panic
// would kill the whole app.
func TestNilManagerIsTolerated(t *testing.T) {
	var m *Manager
	if m.App() != nil {
		t.Error("nil Manager 的 App() 应为 nil")
	}
	if m.Log() != nil {
		t.Error("nil Manager 的 Log() 应为 nil")
	}
	if ids := m.ModuleIDs(); ids != nil {
		t.Errorf("nil Manager 的 ModuleIDs() = %v, 期望 nil", ids)
	}
}

func TestModuleIDsRegisteredFirstInOrder(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(&fakeModule{id: "zeta", name: "Zeta"})
	a.MustRegister(&fakeModule{id: "alpha", name: "Alpha"})

	got := NewManager(a).ModuleIDs()

	// Registered modules come first, in registration order; anything that only
	// exists in config.yaml (the five shipped defaults are always there) is
	// appended after them.
	if len(got) < 2 {
		t.Fatalf("ModuleIDs = %v, 至少应有 2 个注册模块", got)
	}
	want := []string{"zeta", "alpha"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ModuleIDs[%d] = %q, 期望 %q（完整 %v）", i, got[i], want[i], got)
		}
	}
}

func TestModuleIDsDeduplicates(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(&fakeModule{id: "dup", name: "Dup"})

	// Registering the same id twice is rejected by App, but the config file may
	// still list it; ModuleIDs must not surface it twice.
	got := NewManager(a).ModuleIDs()
	count := 0
	for _, id := range got {
		if id == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("模块 dup 出现 %d 次, 期望 1（完整列表 %v）", count, got)
	}
}

func TestModuleIDsIncludesConfigOnlyModules(t *testing.T) {
	a := newTestApp(t)
	a.MustRegister(&fakeModule{id: "live", name: "Live"})

	// A module that is only in config.yaml (removed from the binary, or written
	// by hand) must still appear so its settings are not orphaned in the UI.
	if err := a.Config().Module("orphan").SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	got := NewManager(a).ModuleIDs()
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen["orphan"] {
		t.Errorf("仅存在于配置的模块未出现: %v", got)
	}
	if !seen["live"] {
		t.Errorf("已注册模块未出现: %v", got)
	}
}

func TestModuleIDsEmptyWhenNoModules(t *testing.T) {
	// The default config always ships modules, so the empty case is asserted
	// for a Manager wrapping nothing at all rather than a fresh App.
	var m *Manager
	if ids := m.ModuleIDs(); len(ids) != 0 {
		t.Errorf("空 Manager 的 ModuleIDs = %v, 期望空", ids)
	}
}

func TestShowOnUnsupportedPlatformIsSafe(t *testing.T) {
	// Off Windows Show must warn and return, never panic: main calls it from a
	// tray/menu handler where a panic would take the process down.
	Show(NewManager(newTestApp(t)))
}

func TestShowWithNilManagerDoesNotPanic(t *testing.T) {
	var m *Manager
	Show(m)
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"string", "abc", "abc"},
		{"int", 42, "42"},
		{"bool", true, "true"},
	}
	for _, tc := range cases {
		if got := formatValue(tc.in); got != tc.want {
			t.Errorf("formatValue(%v) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}
