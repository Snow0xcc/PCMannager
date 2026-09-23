package core

import "testing"

// stubModule is a minimal Module used to exercise the Registry.
// It embeds Base so unimplemented methods keep no-op defaults.
type stubModule struct {
	Base
	id string
}

func (s stubModule) ID() string          { return s.id }
func (s stubModule) Name() string        { return "stub " + s.id }
func (s stubModule) Description() string { return "test double" }

// Base only defaults the optional methods; lifecycle hooks must be supplied.
func (s stubModule) Init(*Context) error { return nil }
func (s stubModule) Start() error        { return nil }
func (s stubModule) Stop() error         { return nil }

func TestRegistryRegisterPreservesOrder(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"taskbar", "clipboard", "screenshot"} {
		if err := r.Register(stubModule{id: id}); err != nil {
			t.Fatalf("注册 %s 失败: %v", id, err)
		}
	}

	got := r.IDs()
	want := []string{"taskbar", "clipboard", "screenshot"}
	if len(got) != len(want) {
		t.Fatalf("数量 = %d, 期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("顺序[%d] = %q, 期望 %q", i, got[i], want[i])
		}
	}
}

func TestRegistryRejectsDuplicateAndEmptyID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(stubModule{id: "taskbar"}); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	if err := r.Register(stubModule{id: "taskbar"}); err == nil {
		t.Fatal("重复模块 ID 应报错")
	}
	if err := r.Register(stubModule{id: ""}); err == nil {
		t.Fatal("空模块 ID 应报错")
	}
}

func TestRegistryGetAndAll(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(stubModule{id: "taskbar"})
	r.MustRegister(stubModule{id: "clipboard"})

	m, ok := r.Get("clipboard")
	if !ok {
		t.Fatal("未找到已注册模块")
	}
	if m.ID() != "clipboard" {
		t.Fatalf("ID = %q, 期望 clipboard", m.ID())
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("未注册模块不应被找到")
	}
	if len(r.All()) != 2 {
		t.Fatalf("All() = %d, 期望 2", len(r.All()))
	}
}

func TestRegistrySortedIDs(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"taskbar", "clipboard", "repair"} {
		r.MustRegister(stubModule{id: id})
	}
	got := r.SortedIDs()
	want := []string{"clipboard", "repair", "taskbar"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序[%d] = %q, 期望 %q", i, got[i], want[i])
		}
	}
}

func TestRegistryConcurrentRegister(t *testing.T) {
	r := NewRegistry()
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	done := make(chan struct{})
	for _, id := range ids {
		id := id
		go func() {
			defer func() { done <- struct{}{} }()
			r.MustRegister(stubModule{id: id})
		}()
	}
	for range ids {
		<-done
	}
	if len(r.IDs()) != len(ids) {
		t.Fatalf("注册数 = %d, 期望 %d", len(r.IDs()), len(ids))
	}
}
