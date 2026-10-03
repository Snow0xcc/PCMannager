//go:build windows

package wailsapp

import (
	"sync"
	"sync/atomic"
	"testing"
)

// These tests guard the C4 fix for wailsapp.cancel: the forwarding context's
// CancelFunc is written by OnStartup (one Wails goroutine) and consumed by
// OnShutdown (another) plus the post-Run backstop (the goroutine running
// wails.Run). The pre-fix code shared a plain variable with a nil check, so
// OnShutdown could read a stale value before OnStartup's write landed. The
// cancelGuard must publish it safely and cancel exactly once.

// TestCancelGuardCancelsExactlyOnce is the core regression: concurrent
// callers (OnShutdown + post-Run backstop) must cancel the context exactly
// once, and a call before any set must be a no-op instead of racing a write.
func TestCancelGuardCancelsExactlyOnce(t *testing.T) {
	var calls int32
	var g cancelGuard

	g.set(func() { atomic.AddInt32(&calls, 1) })

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.call()
		}()
	}
	wg.Wait()

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("cancel 被调用 %d 次, 期望恰好 1 次", n)
	}

	// A call before any set must be a no-op (pre-fix this was a racy nil
	// check against a plain variable).
	var empty cancelGuard
	empty.call()
}

// TestCancelGuardConcurrentSetCall exercises the publication race under
// -race: set (OnStartup) concurrent with call (OnShutdown/backstop) must
// never observe a torn value or double-cancel. The final call count depends
// on the scheduler interleaving, so only the absence of a race/panic is
// asserted; every set func is idempotent-safe by counting.
func TestCancelGuardConcurrentSetCall(t *testing.T) {
	var calls int32
	var g cancelGuard
	fn := func() { atomic.AddInt32(&calls, 1) }

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				g.set(fn)
				return
			}
			g.call()
		}(i)
	}
	wg.Wait()

	// A late call after the final set must still cancel (shutdown semantics:
	// the backstop runs after OnShutdown may already have consumed the func,
	// but a set that was never consumed must not be dropped silently).
	g.call()

	if n := atomic.LoadInt32(&calls); n > 2 {
		t.Fatalf("cancel 被调用 %d 次, 至多 2 次（每次 set 至多消费一次）", n)
	}
}
