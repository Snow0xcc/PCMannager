package clipboard

import (
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
	// KindFile is a file-list (CF_HDROP) clipboard entry. Path holds the first
	// dropped file's absolute path; the rest are intentionally dropped.
	KindFile EntryKind = "file"
)

// Entry is a single clipboard item (Ditto-style history entry).
//
// BLOBs never live in memory: image and file entries only keep a cache-file
// path (Path), so a full-screen screenshot costs one struct, not the pixels.
// Entry.Path 为图片/文件类条目保存缓存文件的绝对路径；
// Text 仍承载纯文本；Data 字段删除（保留会诱发全量比较与常驻内存）。
type Entry struct {
	ID        int
	Kind      EntryKind
	Text      string
	Path      string // 图片/文件条目的本地缓存路径（元数据）
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
	onDelete func(Entry)
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

// SetOnDelete registers a callback fired for every evicted or removed entry so
// the caller can unlink its cache file (History stays filesystem agnostic).
// Pinned entries are never deleted, hence never reported here.
func (h *History) SetOnDelete(f func(Entry)) { h.mu.Lock(); h.onDelete = f; h.mu.Unlock() }

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

// AddImageFile appends an image entry whose PNG payload already lives on disk
// at path; size is the file's byte length.
func (h *History) AddImageFile(path string, size int) Entry {
	if path == "" {
		return Entry{}
	}
	return h.add(Entry{Kind: KindImage, Path: path, Size: size})
}

// AddFileEntry appends a file-list entry referencing the dropped file at path
// (absolute, outside the cache dir, so eviction must not unlink it); Text
// carries the file name.
func (h *History) AddFileEntry(path, name string) Entry {
	if path == "" {
		return Entry{}
	}
	return h.add(Entry{Kind: KindFile, Path: path, Text: name})
}

// add appends an entry, de-duplicating against the most recent one.
func (h *History) add(e Entry) Entry {
	if e.Kind == KindText {
		e.Size = len(e.Text) // 文本体量小，就地派生；图片/文件大小由调用方随路径给出
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
// left after them; the oldest unpinned entries are the ones dropped. Evicted
// entries are handed to the delete callback. Caller holds mu.
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
	dropped := make([]Entry, 0, len(h.items))
	for i := len(h.items) - 1; i >= 0; i-- {
		e := h.items[i]
		if e.Pinned {
			kept = append(kept, e)
			continue
		}
		if room > 0 {
			room--
			kept = append(kept, e)
			continue
		}
		dropped = append(dropped, e)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	h.fireDelete(dropped)
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
	var dropped []Entry
	for _, e := range h.items {
		if e.Pinned || e.Timestamp.After(cutoff) {
			kept = append(kept, e)
		} else {
			dropped = append(dropped, e)
		}
	}
	if len(kept) != len(h.items) {
		h.items = kept
		h.fireDelete(dropped)
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
		// Size 由调用方负责（来自缓存文件或拖放文件），这里不再从内存字节推算。
		h.items[i] = e
		h.notifyLocked()
		return
	}
}

// Delete removes one entry by id and reports its cache file for cleanup.
func (h *History) Delete(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.items {
		if e.ID == id {
			h.items = append(h.items[:i], h.items[i+1:]...)
			h.fireDelete([]Entry{e})
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
	var dropped []Entry
	for _, e := range h.items {
		if keep[e.ID] {
			kept = append(kept, e)
		} else {
			dropped = append(dropped, e)
		}
	}
	if len(kept) != len(h.items) {
		h.items = kept
		h.fireDelete(dropped)
		h.notifyLocked()
	}
}

// Clear removes all unpinned entries.
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.items[:0]
	var dropped []Entry
	for _, e := range h.items {
		if e.Pinned {
			kept = append(kept, e)
		} else {
			dropped = append(dropped, e)
		}
	}
	h.items = kept
	h.fireDelete(dropped)
	h.notifyLocked()
}

// fireDelete reports evicted entries to the onDelete callback outside the
// mutex (it runs in its own goroutine, mirroring notifyLocked); caller holds
// mu.
func (h *History) fireDelete(dropped []Entry) {
	if len(dropped) == 0 || h.onDelete == nil {
		return
	}
	go func() {
		for _, e := range dropped {
			h.onDelete(e)
		}
	}()
}

// notifyLocked fires the change callback; caller holds mu.
func (h *History) notifyLocked() {
	if h.onChange != nil {
		go h.onChange()
	}
}

// sameEntry reports whether two entries carry identical payloads.
//
// Images dedupe by cache path + size, files by path: the bytes are never read
// back into memory for comparison -- a full byte compare on every clipboard
// event would stall the watch loop and was exactly the memory bloat this
// storage model removes. Text still compares by full equality.
func sameEntry(a, b Entry) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindImage:
		return a.Path == b.Path && a.Size == b.Size
	case KindFile:
		return a.Path == b.Path
	}
	return a.Text == b.Text
}
