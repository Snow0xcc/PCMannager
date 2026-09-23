//go:build windows

package tray

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// These tests guard P0-1. Before the fix, Show/Hide/updateTooltip held t.mu
// and then called shellNotify, which took t.mu again — a guaranteed
// self-deadlock on the first Show. The lock protocol is now: snapshot t.nid
// under mu, release it, then call the shell through notifyFn (which takes no
// lock at all).
//
// Real Shell_NotifyIconW cannot run in a headless/CI Windows environment, so
// notifyFn is replaced with a recording stub. That still proves the property
// under test (no reentrant locking, plus concurrency safety under -race); it
// does not pretend to validate real Win32 behaviour.

// stubNotify swaps in a fake shell call and restores the original on cleanup.
// It must be called before any goroutine touches the tray.
func stubNotify(t *testing.T) (*int32, *func()) {
	t.Helper()
	var calls int32
	var mu sync.Mutex
	orig := notifyFn
	notifyFn = func(action uint32, nid winui.NOTIFYICONDATAW) error {
		atomic.AddInt32(&calls, 1)
		mu.Lock() // exercises that callers may run concurrently
		mu.Unlock()
		return nil
	}
	restore := func() { notifyFn = orig }
	t.Cleanup(restore)
	return &calls, &restore
}

// newStubTray builds a winTray whose notify always succeeds.
func newStubTray() *winTray {
	return &winTray{
		log:      slog.Default(),
		commands: map[uint32]string{},
	}
}

// TestTrayShowDoesNotDeadlock is the core P0-1 regression: the pre-fix code
// deadlocked here deterministically, so a timeout is a real failure signal.
func TestTrayShowDoesNotDeadlock(t *testing.T) {
	calls, _ := stubNotify(t)
	tr := newStubTray()

	done := make(chan error, 1)
	go func() { done <- tr.Show() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Show 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Show 死锁：5 秒内未返回（P0-1 同锁重入）")
	}

	if !tr.Visible() {
		t.Fatal("Show 后 Visible 应为 true")
	}
	if atomic.LoadInt32(calls) == 0 {
		t.Fatal("Show 应至少调用一次 Shell_NotifyIconW")
	}
}

// TestTrayShowIsIdempotent keeps repeated Show from re-adding the icon.
func TestTrayShowIsIdempotent(t *testing.T) {
	calls, _ := stubNotify(t)
	tr := newStubTray()

	if err := tr.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}
	before := atomic.LoadInt32(calls)
	if err := tr.Show(); err != nil {
		t.Fatalf("第二次 Show: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != before {
		t.Fatalf("重复 Show 又通知了 shell（%d -> %d），应幂等", before, got)
	}
}

// TestTrayHideIsIdempotent covers the same property for Hide.
func TestTrayHideIsIdempotent(t *testing.T) {
	calls, _ := stubNotify(t)
	tr := newStubTray()

	tr.Hide() // no-op before Show
	if atomic.LoadInt32(calls) != 0 {
		t.Fatal("未 Show 时 Hide 不应调用 shell")
	}
}

// TestTraySetMenuConcurrentWithShowHide exercises the required concurrency
// contract: SetMenu may race Show/Hide/Destroy without deadlock or data race
// (P0-1 验收-3).
func TestTraySetMenuConcurrentWithShowHide(t *testing.T) {
	stubNotify(t)
	tr := newStubTray()

	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup

	// SetMenu loop.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			for j := 0; j < 200; j++ {
				tr.SetMenu(Menu{Tooltip: "tip", Items: []Item{{ID: "a", Title: "A"}}})
			}
		}(i)
	}
	// Show/Hide loop.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			for j := 0; j < 200; j++ {
				_ = tr.Show()
				tr.Hide()
			}
		}()
	}
	// Visible readers.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			for j := 0; j < 200; j++ {
				_ = tr.Visible()
			}
		}()
	}

	start.Done()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("并发 SetMenu/Show/Hide 死锁")
	}
}

// TestTrayReAddAfterExplorerRestart simulates WM_SETTINGCHANGE re-adding the
// icon; it must not deadlock and must end up visible again.
func TestTrayReAddAfterExplorerRestart(t *testing.T) {
	stubNotify(t)
	tr := newStubTray()

	if err := tr.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}

	done := make(chan struct{})
	go func() { tr.reAdd(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reAdd 死锁")
	}
}

// TestTrayDestroyReleasesWindow ensures Destroy still drops the window and is
// safe to call (window may be nil in a stubbed environment).
func TestTrayDestroyReleasesWindow(t *testing.T) {
	stubNotify(t)
	tr := newStubTray()

	done := make(chan struct{})
	go func() { tr.Destroy(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Destroy 死锁")
	}
	if tr.Visible() {
		t.Fatal("Destroy 后不应仍可见")
	}
}

// TestTrayNoReentrantLock is a direct structural assertion: notify must not
// acquire t.mu. If a future change reintroduces locking inside notify, this
// deadlocks instead of passing silently.
func TestTrayNoReentrantLock(t *testing.T) {
	stubNotify(t)
	tr := newStubTray()

	// Hold the lock ourselves, then call notify: if notify tried to take mu it
	// would block forever.
	tr.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- tr.notify(winui.NIM_ADD, winui.NOTIFYICONDATAW{})
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		tr.mu.Unlock()
		t.Fatal("notify 试图获取 t.mu（P0-1 重入回归）")
	}
	tr.mu.Unlock()
}
