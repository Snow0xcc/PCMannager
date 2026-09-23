package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests guard P0-3: Publish sends outside Bus.mu while unsubscribe and
// Close close the subscriber channel, so an unguarded send panics with "send
// on closed channel". They are deliberately deterministic — synchronization
// uses barriers and atomic counters, never sleep-then-hope.

// TestBusConcurrentCloseWithPublishers races many publishers against Close
// (prompt 六.9-1). Before the fix this panicked intermittently under -race.
func TestBusConcurrentCloseWithPublishers(t *testing.T) {
	b := NewBus()

	const publishers = 16
	const perPublisher = 200

	// start is a barrier: every goroutine begins publishing at the same
	// instant, maximizing the window where Close lands mid-publish.
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup

	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			for j := 0; j < perPublisher; j++ {
				b.Publish(Event{Type: EventLog, Module: "stress", Message: "x"})
			}
		}(i)
	}

	// A subscriber churns throughout so Close has live channels to close
	// while publishers are mid-send.
	stop := make(chan struct{})
	var churn sync.WaitGroup
	churn.Add(1)
	go func() {
		defer churn.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, unsub := b.Subscribe()
			unsub()
		}
	}()

	start.Done() // release all publishers at once
	b.Close()
	wg.Wait()
	close(stop)
	churn.Wait()

	// Repeated Close must stay safe (六.9-4).
	b.Close()
	b.Close()
}

// TestBusConcurrentSubscribePublishUnsubscribeClose exercises the full
// lifecycle concurrently (六.9-2).
func TestBusConcurrentSubscribePublishUnsubscribeClose(t *testing.T) {
	b := NewBus()

	var closed int32 // counts subscribers that observed a closed channel
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup

	// Subscribers: subscribe, drain briefly, then unsubscribe.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			ch, unsub := b.Subscribe()
			defer unsub()
			for j := 0; j < 50; j++ {
				select {
				case _, ok := <-ch:
					if !ok {
						atomic.AddInt32(&closed, 1)
						return
					}
				default:
				}
			}
		}()
	}

	// Publishers.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			for j := 0; j < 100; j++ {
				b.Publish(Event{Type: EventState, Module: "m", Data: State{"n": j}})
			}
		}(i)
	}

	start.Done()
	wg.Wait()

	// Close after everyone is done; must not panic with live subscribers.
	b.Close()
}

// TestBusUnsubscribeImmediatelyAfterSubscribe targets the history-replay
// window (六.9-3): unsubscribe fires while history is being delivered.
func TestBusUnsubscribeImmediatelyAfterSubscribe(t *testing.T) {
	b := NewBus()

	// Seed history so Subscribe has real work to replay.
	for i := 0; i < 200; i++ {
		b.Publish(Event{Type: EventLog, Module: "hist", Message: "seed"})
	}

	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			_, unsub := b.Subscribe()
			// Unsubscribe at once, racing the pre-filled replay and any
			// concurrent Publish.
			unsub()
			unsub() // idempotent by requirement 五.5
		}()
	}
	// Publish concurrently to maximize interleaving.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			for j := 0; j < 100; j++ {
				b.Publish(Event{Type: EventLog, Module: "live", Message: "y"})
			}
		}()
	}
	start.Done()
	wg.Wait()
	b.Close()
}

// TestBusRepeatedCloseIsSafe covers 六.9-4 and 五.6.
func TestBusRepeatedCloseIsSafe(t *testing.T) {
	b := NewBus()
	ch, unsub := b.Subscribe()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Close()
		}()
	}
	wg.Wait()

	// Subscribers observe closure rather than hanging.
	if _, ok := <-ch; ok {
		// A buffered event may still be pending; drain then expect closure.
		for {
			if _, ok := <-ch; !ok {
				break
			}
		}
	}
	// Double unsubscribe and post-close Subscribe must both be safe.
	unsub()
	unsub()
	ch2, unsub2 := b.Subscribe()
	if _, ok := <-ch2; ok {
		t.Fatal("Close 之后 Subscribe 应返回已关闭的 channel")
	}
	unsub2()
}

// TestBusSubscribeAfterCloseReturnsClosedChannel keeps SSE handlers from
// blocking forever after shutdown (五.4).
func TestBusSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	b := NewBus()
	b.Close()

	ch, unsub := b.Subscribe()
	defer unsub()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("Close 后不应收到事件")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close 后 Subscribe 返回的 channel 应立即关闭，避免 SSE handler 挂住")
	}
}

// TestBusSlowSubscriberNeverBlocksPublish covers 五.3: a subscriber that never
// reads must not stall the publisher.
func TestBusSlowSubscriberNeverBlocksPublish(t *testing.T) {
	b := NewBus()
	defer b.Close()

	// A subscriber that never drains.
	_, unsub := b.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more publishes than the buffer can hold.
		for i := 0; i < 5000; i++ {
			b.Publish(Event{Type: EventLog, Module: "flood", Message: "z"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("慢订阅者阻塞了 Publish")
	}
}

// TestBusHistoryOrderedBeforeLiveEvents verifies the synchronous pre-fill keeps
// replay ordering, which the old async replay goroutine could not guarantee
// (五.8).
func TestBusHistoryOrderedBeforeLiveEvents(t *testing.T) {
	b := NewBus()
	defer b.Close()

	b.Publish(Event{Type: EventLog, Message: "h1"})
	b.Publish(Event{Type: EventLog, Message: "h2"})

	ch, unsub := b.Subscribe()
	defer unsub()

	// Because history is pre-filled under the lock, the first two events read
	// must be the replayed ones, even though we publish immediately after.
	b.Publish(Event{Type: EventLog, Message: "live"})

	var got []string
	for i := 0; i < 3; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev.Message)
		case <-time.After(2 * time.Second):
			t.Fatalf("只收到 %d 个事件，期望 3", len(got))
		}
	}
	if got[0] != "h1" || got[1] != "h2" || got[2] != "live" {
		t.Fatalf("事件顺序 = %v, 期望 [h1 h2 live]", got)
	}
}
