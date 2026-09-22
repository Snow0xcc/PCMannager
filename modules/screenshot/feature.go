package screenshot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	clip "golang.design/x/clipboard"
	"github.com/kbinani/screenshot"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Feature implements a Snipaste-style screenshot tool (default hotkey F1).
type Feature struct {
	app *core.App
}

// NewFeature constructs the screenshot feature.
func NewFeature() *Feature { return &Feature{} }

func (f *Feature) Name() string  { return "screenshot" }
func (f *Feature) Title() string { return "截图工具 (Snipaste)" }

func (f *Feature) Init(app *core.App) error {
	f.app = app
	return nil
}

func (f *Feature) Start() error {
	if !f.app.FeatureCfg("screenshot").Enabled {
		return nil
	}
	f.app.Log.Infof("screenshot feature ready (F1)")
	return nil
}

func (f *Feature) Stop() error { return nil }

// OnHotkey captures the primary display and opens the region editor.
func (f *Feature) OnHotkey() error {
	go f.captureAndEdit()
	return nil
}

// OpenUI is the same as the hotkey action for this feature.
func (f *Feature) OpenUI() error { return f.OnHotkey() }

// captureAndEdit grabs the primary monitor and launches the editor window.
func (f *Feature) captureAndEdit() {
	if err := clip.Init(); err != nil {
		f.app.Log.Errorf("clipboard init for screenshot: %v", err)
	}
	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		f.app.Log.Warnf("no active display found")
		return
	}
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		f.app.Log.Errorf("capture: %v", err)
		return
	}
	openEditor(f.app, img, bounds, f.save, f.copyImage)
}

// save writes the cropped image to the user's Pictures folder.
func (f *Feature) save(img image.Image) {
	dir, err := os.UserHomeDir()
	if err == nil {
		dir = filepath.Join(dir, "Pictures", "PCMannager")
		_ = os.MkdirAll(dir, 0o755)
	} else {
		dir = "."
	}
	name := filepath.Join(dir, fmt.Sprintf("screenshot_%d.png", time.Now().UnixMilli()))
	f.writePNG(name, img)
}

// copyImage pushes the cropped image onto the clipboard as PNG.
func (f *Feature) copyImage(img image.Image) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		f.app.Log.Errorf("png encode: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := clip.Write(ctx, clip.FmtImage, buf.Bytes()); err != nil {
		f.app.Log.Errorf("clipboard image write: %v", err)
	}
}

func (f *Feature) writePNG(path string, img image.Image) {
	fp, err := os.Create(path)
	if err != nil {
		f.app.Log.Errorf("create %s: %v", path, err)
		return
	}
	defer fp.Close()
	if err := png.Encode(fp, img); err != nil {
		f.app.Log.Errorf("encode %s: %v", path, err)
		return
	}
	f.app.Log.Infof("screenshot saved: %s", path)
}
