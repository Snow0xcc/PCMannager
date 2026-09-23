package core

import (
	"sync"
	"testing"
	"time"
)

// TestBusPublishDelivers verifies a subscriber receives published events.
func TestBusPublishDelivers(t *testing.T) {
	b := NewBus()
	defer b.Close()

	ch, unsubscribe := b.Subscribe()
	defer unsubscribe()

	b.Log("taskbar", "info", "hello")

	select {
	case ev := <-ch:
		if ev.Type != EventLog {
			t.Fatalf("事件类型 = %q, 期望 %q", ev.Type, EventLog)
		}
		if ev.Module != "taskbar" || ev.Message != "hello" {
			t.Fatalf("事件内容不符: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者未收到事件")
	}
}

// TestBusReplaysHistory checks a late subscriber is caught up with recent events.
func TestBusReplaysHistory(t *testing.T) {
	b := NewBus()
	defer b.Close()

	b.State("clipboard", State{"running": true})
	b.Notice("repair", "done")

	ch, unsubscribe := b.Subscribe()
	defer unsubscribe()

	// Replay is asynchronous, so allow a short grace period.
	got := make([]Event, 0, 2)
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev := <-ch:
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("历史重放不足: 只有 %d 条", len(got))
		}
	}
	if got[0].Type != EventState || got[1].Type != EventNotice {
		t.Fatalf("重放顺序不符: %+v", got)
	}
}

// TestBusPublishNeverBlocks ensures a full subscriber cannot stall a publisher.
func TestBusPublishNeverBlocks(t *testing.T) {
	b := NewBus()
	defer b.Close()

	// Subscribe but never drain: the channel buffer must not block Publish.
	_, unsubscribe := b.Subscribe()
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Overflow the 256-slot buffer many times over.
		for i := 0; i < 2000; i++ {
			b.Log("taskbar", "info", "spam")
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish 被慢订阅者阻塞")
	}
}

// TestBusCloseReleasesSubscribers checks Close unblocks and closes channels.
func TestBusCloseReleasesSubscribers(t *testing.T) {
	b := NewBus()
	ch, _ := b.Subscribe()

	b.Close()

	if _, open := <-ch; open {
		// A closed channel may still hold buffered events; drain until closed.
		deadline := time.After(time.Second)
		for {
			select {
			case _, open := <-ch:
				if !open {
					return
				}
			case <-deadline:
				t.Fatal("Close 后通道未关闭")
			}
		}
	}
}

// TestBusCloseIdempotent guards against a double close panicking on shutdown.
func TestBusCloseIdempotent(t *testing.T) {
	b := NewBus()
	b.Close()
	b.Close() // must not panic
}

// TestBusProgressPayload checks progress events carry action/percent/line.
func TestBusProgressPayload(t *testing.T) {
	b := NewBus()
	defer b.Close()

	ch, unsubscribe := b.Subscribe()
	defer unsubscribe()

	b.Progress("repair", "install_git", 42, "downloading")

	select {
	case ev := <-ch:
		if ev.Type != EventProgress {
			t.Fatalf("类型 = %q, 期望 %q", ev.Type, EventProgress)
		}
		if ev.Data["action"] != "install_git" || ev.Data["percent"] != 42 {
			t.Fatalf("进度数据不符: %+v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到进度事件")
	}
}

// TestBusConcurrentPublish exercises the bus under concurrent publishers.
func TestBusConcurrentPublish(t *testing.T) {
	b := NewBus()
	defer b.Close()

	ch, unsubscribe := b.Subscribe()
	defer unsubscribe()

	// Drain so the publisher never hits the drop path.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ch:
			case <-stop:
				return
			}
		}
	}()

	var pub sync.WaitGroup
	for i := 0; i < 8; i++ {
		pub.Add(1)
		go func() {
			defer pub.Done()
			for j := 0; j < 100; j++ {
				b.Log("taskbar", "info", "tick")
			}
		}()
	}
	pub.Wait()
	close(stop)
	wg.Wait()
}
