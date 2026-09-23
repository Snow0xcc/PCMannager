package core

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeBackend records what the Manager asked it to do, and simulates the
// threaded backend's blocking behaviour: every call waits on a channel, so a
// Manager that calls it while holding its own mutex would deadlock with a
// concurrent caller. That is exactly the P0-2 regression being guarded.
type fakeBackend struct {
	mu sync.Mutex

	registered   []int
	unregistered []int
	closed       int

	// failNext makes the next register call fail, to exercise rollback.
	failNext bool

	// block is signalled while a backend call is in flight.
	inCall chan struct{}
	// release lets the test hold a backend call open.
	release chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{inCall: make(chan struct{}, 8), release: make(chan struct{})}
}

// enter simulates a blocking backend round-trip to another thread.
func (f *fakeBackend) enter() {
	f.inCall <- struct{}{}
	<-f.release
}

func (f *fakeBackend) register(id int, c Combo) error {
	f.mu.Lock()
	fail := f.failNext
	f.failNext = false
	f.mu.Unlock()

	f.enter()

	f.mu.Lock()
	defer f.mu.Unlock()
	if fail {
		return errors.New("fake: 注册失败")
	}
	f.registered = append(f.registered, id)
	return nil
}

func (f *fakeBackend) unregister(id int) error {
	f.enter()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregistered = append(f.unregistered, id)
	return nil
}

func (f *fakeBackend) run(ch chan<- int) { <-make(chan struct{}) }

func (f *fakeBackend) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
}

func (f *fakeBackend) snapshot() (reg, unreg []int, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.registered...), append([]int(nil), f.unregistered...), f.closed
}

// drain lets every pending backend call proceed.
func (f *fakeBackend) drain() {
	for {
		select {
		case <-f.inCall:
		default:
			return
		}
		select {
		case f.release <- struct{}{}:
		case <-time.After(2 * time.Second):
			return
		}
	}
}

// newManagerWithFake builds a Manager whose backend is the fake, mirroring how
// a threaded (Windows) backend behaves without touching Win32.
func newManagerWithFake() (*HotkeyManager, *fakeBackend) {
	h := NewHotkeyManager(nil)
	fb := newFakeBackend()
	h.backend = fb
	// Let calls through by default: release one waiter per pending call.
	go func() {
		for {
			<-fb.inCall
			fb.release <- struct{}{}
		}
	}()
	return h, fb
}

// TestHotkeyBindRegistersWithBackend verifies Bind reaches the backend.
func TestHotkeyBindRegistersWithBackend(t *testing.T) {
	h, fb := newManagerWithFake()

	h.Bind("m1", "ctrl+alt+f1", nil)

	reg, _, _ := fb.snapshot()
	if len(reg) != 1 {
		t.Fatalf("注册 %d 次, 期望 1", len(reg))
	}
	if _, ok := h.Combo("m1"); !ok {
		t.Fatal("绑定后 Combo 应可用")
	}
}

// TestHotkeyBindDoesNotHoldLockDuringBackendCall is the core P0-2 regression:
// the backend call must happen outside h.mu, otherwise a concurrent Bind or
// Stop stalls behind a slow backend round-trip.
func TestHotkeyBindDoesNotHoldLockDuringBackendCall(t *testing.T) {
	h := NewHotkeyManager(nil)
	fb := newFakeBackend()
	h.backend = fb

	// Start a Bind that will park inside the backend call.
	go h.Bind("m1", "ctrl+alt+f1", nil)
	select {
	case <-fb.inCall:
	case <-time.After(2 * time.Second):
		t.Fatal("Bind 未调用后端")
	}

	// While that call is parked, other Manager read operations must still work.
	// They all take h.mu, so completing them proves the parked backend call is
	// NOT holding it. (Unbind is deliberately excluded: it also enters the
	// backend and would park here for reasons unrelated to the lock.)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.Conflicts()
		_, _ = h.Combo("m1")
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("后端调用期间 Manager 被锁住（P0-2：不应持锁调用后端）")
	}

	// Release the parked call.
	fb.release <- struct{}{}
}

// TestHotkeyRebindUnregistersOld drops the previous registration first.
func TestHotkeyRebindUnregistersOld(t *testing.T) {
	h, fb := newManagerWithFake()

	h.Bind("m1", "ctrl+alt+f1", nil)
	first, _, _ := fb.snapshot()
	if len(first) != 1 {
		t.Fatalf("首次注册 %d 次", len(first))
	}
	id1 := first[0]

	h.Bind("m1", "ctrl+alt+f2", nil)

	reg, unreg, _ := fb.snapshot()
	if len(unreg) == 0 || unreg[0] != id1 {
		t.Fatalf("重新绑定应先注销旧 id %d, 实际 %v", id1, unreg)
	}
	if len(reg) != 2 {
		t.Fatalf("应注册两次, 实际 %v", reg)
	}
}

// TestHotkeyBindFailureRollsBack keeps a failed registration from leaving the
// module marked as bound (and from leaking a slot into the conflict table).
func TestHotkeyBindFailureRollsBack(t *testing.T) {
	h, fb := newManagerWithFake()
	fb.failNext = true

	var warned []string
	h.warn = func(f string, a ...any) { warned = append(warned, f) }

	h.Bind("m1", "ctrl+alt+f1", nil)

	if _, ok := h.Combo("m1"); ok {
		t.Fatal("注册失败后不应保留 combo（回滚失败）")
	}
	if len(warned) == 0 {
		t.Fatal("注册失败应通过 warn 上报")
	}

	// A different module may now claim the same combo, proving the slot was
	// released rather than leaked.
	h.Bind("m2", "ctrl+alt+f1", nil)
	if _, ok := h.Combo("m2"); !ok {
		t.Fatal("回滚后其它模块应能占用同一热键")
	}
}

// TestHotkeyUnbindReachesBackend verifies unregistration is requested.
func TestHotkeyUnbindReachesBackend(t *testing.T) {
	h, fb := newManagerWithFake()

	h.Bind("m1", "ctrl+alt+f1", nil)
	h.Unbind("m1")

	reg, unreg, _ := fb.snapshot()
	if len(reg) != 1 || len(unreg) != 1 || unreg[0] != reg[0] {
		t.Fatalf("Unbind 未注销对应 id: reg=%v unreg=%v", reg, unreg)
	}
	if _, ok := h.Combo("m1"); ok {
		t.Fatal("Unbind 后不应保留 combo")
	}
}

// TestHotkeyStopUnregistersAllThenCloses asserts the shutdown order required by
// P0-2: every hotkey is unregistered while the backend is still alive, and
// close is called exactly once.
func TestHotkeyStopUnregistersAllThenCloses(t *testing.T) {
	h, fb := newManagerWithFake()

	h.Bind("m1", "ctrl+alt+f1", nil)
	h.Bind("m2", "ctrl+alt+f2", nil)
	h.Stop()

	reg, unreg, closed := fb.snapshot()
	if len(reg) != 2 {
		t.Fatalf("应注册 2 个, 实际 %v", reg)
	}
	if len(unreg) != 2 {
		t.Fatalf("Stop 应注销全部 2 个, 实际 %v", unreg)
	}
	if closed != 1 {
		t.Fatalf("close 调用 %d 次, 期望 1", closed)
	}
}

// TestHotkeyStopTwiceClosesTwiceButNeverPanics mirrors the backend's own
// idempotency requirement (the Manager always calls close).
func TestHotkeyStopTwiceClosesTwiceButNeverPanics(t *testing.T) {
	h, fb := newManagerWithFake()
	h.Bind("m1", "ctrl+alt+f1", nil)
	h.Stop()
	h.Stop()

	_, _, closed := fb.snapshot()
	if closed != 2 {
		t.Fatalf("两次 Stop -> close %d 次, 期望 2", closed)
	}
}

// TestHotkeyConflictsStillReported keeps the duplicate-id rejection intact
// after the locking change.
func TestHotkeyConflictsStillReported(t *testing.T) {
	h, _ := newManagerWithFake()

	var warned []string
	h.warn = func(f string, a ...any) { warned = append(warned, f) }

	h.Bind("m1", "ctrl+alt+f1", nil)
	h.Bind("m2", "ctrl+alt+f1", nil) // duplicate

	if len(warned) == 0 {
		t.Fatal("重复热键应告警")
	}
	if _, ok := h.Combo("m2"); ok {
		t.Fatal("重复热键不应被第二个模块占用")
	}
}

// TestHotkeyManualHandlerSurvivesRegistrationFailure keeps tray/panel paths
// working even when the OS refuses a hotkey (requirement 四.7).
func TestHotkeyManualHandlerSurvivesRegistrationFailure(t *testing.T) {
	h, _ := newManagerWithFake()

	called := false
	h.Bind("m1", "ctrl+alt+f1", func() error { called = true; return nil })

	// The handler is retained so manual invocation still works.
	h.mu.Lock()
	fn := h.handlers["m1"]
	h.mu.Unlock()
	if fn == nil {
		t.Fatal("处理器应被保留")
	}
	if err := fn(); err != nil {
		t.Fatalf("手动调用: %v", err)
	}
	if !called {
		t.Fatal("手动调用未触发处理器")
	}
}

// TestHotkeyConcurrentBindUnbindStop exercises the Manager under -race with
// the threaded-style blocking backend.
func TestHotkeyConcurrentBindUnbindStop(t *testing.T) {
	h, fb := newManagerWithFake()

	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			for j := 0; j < 25; j++ {
				h.Bind("mod", "ctrl+alt+f1", nil)
				h.Unbind("mod")
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		start.Wait()
		for j := 0; j < 25; j++ {
			_ = h.Conflicts()
			_, _ = h.Combo("mod")
		}
	}()

	start.Done()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("并发 Bind/Unbind 死锁")
	}

	h.Stop()
	_ = fb
}
