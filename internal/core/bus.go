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
type subscriber struct {
	ch   chan Event
	once sync.Once
}

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
		select {
		case s.ch <- ev:
		default: // drop: subscriber is too slow
		}
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

// Subscribe registers a new subscriber and replays recent history to it.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, 256)}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(s.ch)
		return s.ch, func() {}
	}
	b.subs[s] = struct{}{}
	hist := make([]Event, len(b.history))
	copy(hist, b.history)
	b.mu.Unlock()

	// Replay asynchronously so Subscribe never blocks on a slow reader.
	go func() {
		for _, ev := range hist {
			select {
			case s.ch <- ev:
			default:
				return
			}
		}
	}()

	return s.ch, func() {
		s.once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s)
			b.mu.Unlock()
			close(s.ch)
		})
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
		s.once.Do(func() { close(s.ch) })
	}
}
