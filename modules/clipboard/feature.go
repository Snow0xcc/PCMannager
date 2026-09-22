package clipboard

import (
	"context"
	"strings"
	"time"

	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Feature implements a Ditto-style clipboard history manager.
type Feature struct {
	app     *core.App
	hist    *History
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// NewFeature constructs the clipboard manager feature.
func NewFeature() *Feature {
	return &Feature{hist: NewHistory(200)}
}

func (f *Feature) Name() string  { return "clipboard" }
func (f *Feature) Title() string { return "剪贴板管理器 (Ditto)" }

func (f *Feature) Init(app *core.App) error {
	f.app = app
	// Clipboard requires a GUI context; init lazily in Start on Windows.
	return nil
}

func (f *Feature) Start() error {
	if !f.app.FeatureCfg("clipboard").Enabled {
		return nil
	}
	if err := clip.Init(); err != nil {
		f.app.Log.Errorf("clipboard.Init: %v", err)
		return err
	}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.running = true
	go f.watch()
	f.app.Log.Infof("clipboard manager started")
	return nil
}

// watch monitors the system clipboard and records text changes.
func (f *Feature) watch() {
	ch := clip.Watch(f.ctx, clip.FmtText)
	for {
		select {
		case <-f.ctx.Done():
			return
		case d := <-ch:
			if d.Format == clip.FmtText && len(d.Bytes) > 0 {
				text := strings.TrimRight(string(d.Bytes), "\r\n")
				f.hist.Add(text)
			}
		}
	}
}

func (f *Feature) Stop() error {
	f.running = false
	if f.cancel != nil {
		f.cancel()
	}
	return nil
}

// OnHotkey opens the clipboard history viewer (Ditto-style popup).
func (f *Feature) OnHotkey() error {
	return f.OpenUI()
}

// OpenUI shows the clipboard history window.
func (f *Feature) OpenUI() error {
	go showViewer(f.hist, f.app, f.writeBack)
	return nil
}

// writeBack pushes a chosen entry back onto the system clipboard.
func (f *Feature) writeBack(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := clip.Write(ctx, clip.FmtText, []byte(text)); err != nil {
		f.app.Log.Errorf("clipboard write-back: %v", err)
	}
}
