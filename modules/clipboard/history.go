package clipboard

import (
	"sync"
	"time"
)

// Entry is a single clipboard item (Ditto-style history entry).
type Entry struct {
	ID        int
	Text      string
	Timestamp time.Time
	Pinned    bool
}

// History keeps a bounded, deduplicated ring of clipboard entries.
type History struct {
	mu       sync.Mutex
	items    []Entry
	seq      int
	max      int
	onChange func()
}

// NewHistory creates a history bounded to max entries.
func NewHistory(max int) *History {
	if max <= 0 {
		max = 200
	}
	return &History{max: max, items: make([]Entry, 0, max)}
}

// SetOnChange registers a callback fired whenever the history is modified.
func (h *History) SetOnChange(f func()) { h.mu.Lock(); h.onChange = f; h.mu.Unlock() }

// Add appends a new entry, dropping duplicates (by text) when not pinned.
func (h *History) Add(text string) {
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// dedupe against the most recent entry
	if len(h.items) > 0 && h.items[len(h.items)-1].Text == text {
		return
	}
	h.seq++
	e := Entry{ID: h.seq, Text: text, Timestamp: time.Now()}
	h.items = append(h.items, e)
	if len(h.items) > h.max {
		h.items = h.items[len(h.items)-h.max:]
	}
	if h.onChange != nil {
		go h.onChange()
	}
}

// All returns a copy of the entries, newest last.
func (h *History) All() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Entry, len(h.items))
	copy(out, h.items)
	return out
}

// Get returns the entry with the given id.
func (h *History) Get(id int) (Entry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.items {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Clear removes all non-pinned entries.
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.items[:0]
	for _, e := range h.items {
		if e.Pinned {
			kept = append(kept, e)
		}
	}
	h.items = kept
	if h.onChange != nil {
		go h.onChange()
	}
}
