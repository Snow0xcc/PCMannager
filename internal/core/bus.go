package core

import (
	"sync"
	"time"
)

// Event is a single message pushed to the panel over SSE.
type Event struct {
	// Type is one of "log", "state", "progress", "notice".
	Type    string    `json:"type"`
	Module  string    `json:"module,omitempty"`
	Level   string    `json:"level,omitempty"`
	Message string    `json:"message,omitempty"`
	Data    State     `json:"data,omitempty"`
	Time    time.Time `json:"time"`
}

// Event type constants.
const (
	EventLog      = "log"
	EventState    = "state"
	EventProgress = "progress"
	EventNotice   = "notice"
)

// subscriber is one SSE client. slow clients drop events rather than block.
//
// Synchronization protocol (P0-3): sending and closing race with each other,
// because Publish copies the subscriber set under Bus.mu and then sends
// outside that lock, while unsubscribe/Close close the channel concurrently.
// A bare `select { case ch <- ev: }` could therefore panic with "send on
// closed channel". Every send now goes through send(), which is serialized
// against close() by the subscriber's own mutex and gated on `done`.
//
// Lock order is always Bus.mu -> subscriber.mu, never the reverse.
type subscriber struct {
	ch   chan Event
	mu   sync.Mutex
	done bool
}

// send delivers ev without blocking. It reports false when the subscriber was
// already closed or its buffer is full (a slow client drops events).
func (s *subscriber) send(ev Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	select {
	case s.ch <- ev:
		return true
	default:
		return false // drop: subscriber is too slow
	}
}

// close closes the channel exactly once; further calls are no-ops, so
// unsubscribe stays idempotent and repeated Close is safe.
func (s *subscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	close(s.ch)
}

// subBuffer is the per-subscriber channel capacity. It is deliberately larger
// than maxHist so history can be pre-filled without ever blocking.
const subBuffer = 256

// Bus is a fan-out event broadcaster with per-subscriber buffering.
// Events are dropped for subscribers that cannot keep up, so a stalled
// browser tab can never stall a module.
type Bus struct {
	mu     sync.RWMutex
	subs   map[*subscriber]struct{}
	closed bool
	// history keeps the most recent events so a freshly opened panel has context.
	history []Event
	maxHist int
}

// NewBus creates an empty event bus.
func NewBus() *Bus {
	return &Bus{subs: map[*subscriber]struct{}{}, maxHist: 200}
}

// Publish broadcasts an event. It never blocks.
func (b *Bus) Publish(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.history = append(b.history, ev)
	if len(b.history) > b.maxHist {
		b.history = b.history[len(b.history)-b.maxHist:]
	}
	subs := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		s.send(ev)
	}
}

// Log publishes a log level event attributed to a module.
func (b *Bus) Log(module, level, msg string) {
	b.Publish(Event{Type: EventLog, Module: module, Level: level, Message: msg})
}

// Progress publishes an install/cleanup progress update (0..100, -1 unknown).
func (b *Bus) Progress(module, action string, percent int, line string) {
	b.Publish(Event{Type: EventProgress, Module: module, Data: State{
		"action":  action,
		"percent": percent,
		"line":    line,
	}})
}

// Notice publishes a user-facing notification.
func (b *Bus) Notice(module, msg string) {
	b.Publish(Event{Type: EventNotice, Module: module, Message: msg})
}

// State publishes a module state change so open panels can refresh.
func (b *Bus) State(module string, data State) {
	b.Publish(Event{Type: EventState, Module: module, Data: data})
}

// Subscribe registers a new subscriber and pre-fills it with recent history.
//
// History is filled synchronously under Bus.mu (through send()) rather than by
// a detached goroutine: an async replay would send after Subscribe returned,
// racing with unsubscribe/Close and panicking on a closed channel. Because the
// buffer (subBuffer) exceeds maxHist, pre-filling can never block — so this
// keeps Subscribe non-blocking without an unbounded queue.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, subBuffer)}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		s.close()
		return s.ch, func() {}
	}
	b.subs[s] = struct{}{}
	// Pre-fill history while holding Bus.mu, so replayed events are ordered
	// before any live event published after Subscribe returns.
	for _, ev := range b.history {
		s.send(ev)
	}
	b.mu.Unlock()

	return s.ch, func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
		s.close()
	}
}

// Close shuts the bus down and releases all subscribers.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.subs = map[*subscriber]struct{}{}
	b.mu.Unlock()
	for _, s := range subs {
		s.close()
	}
}
