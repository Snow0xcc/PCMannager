package selfcontext

import (
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Feature implements a MineContext-lite: it records the active window title
// over time so the user can review "what I was working on" and paste a
// compact context summary into an LLM.
type Feature struct {
	app      *core.App
	mu       sync.Mutex
	entries  []Entry
	stopCh   chan struct{}
	running  bool
}

// Entry is one sampled active-window context record.
type Entry struct {
	Title     string
	Timestamp time.Time
}

// NewFeature constructs the selfcontext feature.
func NewFeature() *Feature { return &Feature{} }

func (f *Feature) Name() string  { return "selfcontext" }
func (f *Feature) Title() string { return "上下文记录 (MineContext)" }

func (f *Feature) Init(app *core.App) error {
	f.app = app
	return nil
}

func (f *Feature) Start() error {
	if !f.app.FeatureCfg("selfcontext").Enabled {
		return nil
	}
	f.stopCh = make(chan struct{})
	f.running = true
	go f.sample()
	f.app.Log.Infof("selfcontext recorder started")
	return nil
}

// sample records the active window title every 1s, deduping repeats.
func (f *Feature) sample() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-f.stopCh:
			return
		case <-ticker.C:
			title, err := ActiveTitle()
			if err != nil || title == "" {
				continue
			}
			f.mu.Lock()
			n := len(f.entries)
			if n == 0 || f.entries[n-1].Title != title {
				f.entries = append(f.entries, Entry{Title: title, Timestamp: time.Now()})
				if len(f.entries) > 500 {
					f.entries = f.entries[len(f.entries)-500:]
				}
			}
			f.mu.Unlock()
		}
	}
}

func (f *Feature) Stop() error {
	f.running = false
	if f.stopCh != nil {
		close(f.stopCh)
	}
	return nil
}

// Snapshot returns a copy of the recorded context entries.
func (f *Feature) Snapshot() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Entry, len(f.entries))
	copy(out, f.entries)
	return out
}

// Summary renders a compact plain-text context summary (for pasting to an LLM).
func (f *Feature) Summary() string {
	es := f.Snapshot()
	if len(es) == 0 {
		return "(无记录)"
	}
	out := "最近的工作上下文：\n"
	for _, e := range es {
		out += "- " + e.Timestamp.Format("15:04:05") + " " + e.Title + "\n"
	}
	return out
}

// OnHotkey opens the context viewer.
func (f *Feature) OnHotkey() error { return f.OpenUI() }

// OpenUI shows the context review window.
func (f *Feature) OpenUI() error {
	go showContext(f)
	return nil
}
