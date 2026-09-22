package clipboard

import (
	"bytes"
	"sync"
	"time"
)

// EntryKind distinguishes the payload stored in an entry.
type EntryKind string

const (
	// KindText is a plain-text clipboard entry.
	KindText EntryKind = "text"
	// KindImage is a PNG-encoded image clipboard entry.
	KindImage EntryKind = "image"
)

// Entry is a single clipboard item (Ditto-style history entry).
type Entry struct {
	ID   int
	Kind EntryKind
	Text string
	// Data holds the raw PNG bytes for KindImage entries.
	Data      []byte
	Size      int
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
		max = defaultMaxItems
	}
	return &History{max: max, items: make([]Entry, 0, max)}
}

// SetOnChange registers a callback fired whenever the history is modified.
func (h *History) SetOnChange(f func()) { h.mu.Lock(); h.onChange = f; h.mu.Unlock() }

// Resize changes the history bound, trimming immediately when it shrinks.
func (h *History) Resize(max int) {
	if max <= 0 {
		max = defaultMaxItems
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.max = max
	if len(h.items) > max {
		h.items = h.trimLocked()
	}
}

// AddText appends a text entry, dropping an identical neighbour at the head.
func (h *History) AddText(text string) Entry {
	if text == "" {
		return Entry{}
	}
	return h.add(Entry{Kind: KindText, Text: text})
}

// AddImage appends an image entry, dropping an identical neighbour at the head.
func (h *History) AddImage(png []byte) Entry {
	if len(png) == 0 {
		return Entry{}
	}
	return h.add(Entry{Kind: KindImage, Data: png})
}

// add appends an entry, de-duplicating against the most recent one.
func (h *History) add(e Entry) Entry {
	if e.Kind == KindImage {
		e.Size = len(e.Data)
	} else {
		e.Size = len(e.Text)
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if n := len(h.items); n > 0 && sameEntry(h.items[n-1], e) {
		// Re-copying the same content: refresh its timestamp in place so the
		// history keeps its order instead of growing a duplicate.
		h.items[n-1].Timestamp = time.Now()
		return h.items[n-1]
	}

	h.seq++
	e.ID = h.seq
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	h.items = append(h.items, e)
	if len(h.items) > h.max {
		h.items = h.trimLocked()
	}
	h.notifyLocked()
	return e
}

// trimLocked keeps the newest h.max entries, never evicting a pinned one.
//
// Pinned entries are immortal, so the budget for unpinned entries is what is
// left after them; the oldest unpinned entries are the ones dropped. Caller
// holds mu.
func (h *History) trimLocked() []Entry {
	pinned := 0
	for _, e := range h.items {
		if e.Pinned {
			pinned++
		}
	}
	room := h.max - pinned
	if room < 0 {
		room = 0
	}

	// Walk from the newest backwards, taking every pinned entry plus up to
	// `room` unpinned ones, then restore chronological order.
	kept := make([]Entry, 0, h.max)
	for i := len(h.items) - 1; i >= 0; i-- {
		e := h.items[i]
		if e.Pinned {
			kept = append(kept, e)
			continue
		}
		if room > 0 {
			room--
			kept = append(kept, e)
		}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}

// PruneOlder removes unpinned entries captured before cutoff.
func (h *History) PruneOlder(cutoff time.Time) {
	if cutoff.IsZero() {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.items[:0]
	for _, e := range h.items {
		if e.Pinned || e.Timestamp.After(cutoff) {
			kept = append(kept, e)
		}
	}
	if len(kept) != len(h.items) {
		h.items = kept
		h.notifyLocked()
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

// Update replaces an entry in place, keeping its identity and timestamp.
func (h *History) Update(e Entry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.items {
		if h.items[i].ID != e.ID {
			continue
		}
		if e.Kind == KindImage {
			e.Size = len(e.Data)
		} else {
			e.Size = len(e.Text)
		}
		h.items[i] = e
		h.notifyLocked()
		return
	}
}

// Delete removes one entry by id.
func (h *History) Delete(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.items {
		if e.ID == id {
			h.items = append(h.items[:i], h.items[i+1:]...)
			h.notifyLocked()
			return
		}
	}
}

// DeleteExcept removes every entry except those with the given ids.
func (h *History) DeleteExcept(keep map[int]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.items[:0]
	for _, e := range h.items {
		if keep[e.ID] {
			kept = append(kept, e)
		}
	}
	if len(kept) != len(h.items) {
		h.items = kept
		h.notifyLocked()
	}
}

// Clear removes all unpinned entries.
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
	h.notifyLocked()
}

// notifyLocked fires the change callback; caller holds mu.
func (h *History) notifyLocked() {
	if h.onChange != nil {
		go h.onChange()
	}
}

// sameEntry reports whether two entries carry identical payloads.
func sameEntry(a, b Entry) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == KindImage {
		return bytes.Equal(a.Data, b.Data)
	}
	return a.Text == b.Text
}
