package screenshot

import (
	"image"
	"image/color"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// fakeConfig is a minimal ModuleConfig so option-driven helpers can be
// exercised without loading a real config file.
type fakeConfig struct {
	mu   sync.Mutex
	opts map[string]any
}

func (c *fakeConfig) Enabled() bool  { return true }
func (c *fakeConfig) Hotkey() string { return "" }
func (c *fakeConfig) Get(key string, def any) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.opts[key]; ok {
		return v
	}
	return def
}
func (c *fakeConfig) Set(key string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opts == nil {
		c.opts = map[string]any{}
	}
	c.opts[key] = value
	return nil
}
func (c *fakeConfig) SetEnabled(bool) error  { return nil }
func (c *fakeConfig) SetHotkey(string) error { return nil }

// newTestFeature builds a Feature with a context wired to a temp data dir.
func newTestFeature(t *testing.T, opts map[string]any) (*Feature, *core.Context) {
	t.Helper()
	dir := t.TempDir()
	ctx := &core.Context{
		Ctx:     t.Context(),
		Logger:  testLogger(),
		Config:  &fakeConfig{opts: opts},
		DataDir: dir,
	}
	f := &Feature{ctx: ctx}
	return f, ctx
}

// testLogger keeps log output out of the test run while still satisfying the
// module's logging calls.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestFormatDefaultsToPNG verifies an unset/absent format falls back to png.
func TestFormatDefaultsToPNG(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	if got := f.format(); got != "png" {
		t.Fatalf("format = %q, 期望 png", got)
	}
	if got := f.ext(); got != ".png" {
		t.Fatalf("ext = %q, 期望 .png", got)
	}
}

// TestFormatRejectsUnknown values degrade to png rather than writing garbage.
func TestFormatRejectsUnknown(t *testing.T) {
	f, _ := newTestFeature(t, map[string]any{optFormat: "gif"})
	if got := f.format(); got != "png" {
		t.Fatalf("未知格式应回退 png, 实际 %q", got)
	}
}

// TestFormatJPG switches both the extension and the encoder path.
func TestFormatJPG(t *testing.T) {
	f, _ := newTestFeature(t, map[string]any{optFormat: "jpg"})
	if got := f.format(); got != "jpg" {
		t.Fatalf("format = %q, 期望 jpg", got)
	}
	if got := f.ext(); got != ".jpg" {
		t.Fatalf("ext = %q, 期望 .jpg", got)
	}
}

// TestQualityClamps into [10,100] and falls back when absent or out of range.
func TestQualityClamps(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{nil, defaultJPGQuality},
		{50, 50},
		{5, defaultJPGQuality},   // below the floor
		{500, defaultJPGQuality}, // above the ceiling
		{float64(75), 75},        // YAML decodes numbers as float64
	}
	for _, c := range cases {
		opts := map[string]any{}
		if c.in != nil {
			opts[optJPGQuality] = c.in
		}
		f, _ := newTestFeature(t, opts)
		if got := f.quality(); got != c.want {
			t.Errorf("quality(%v) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestSaveDirPrefersConfigured checks the precedence: configured dir wins,
// then the module data dir.
func TestSaveDirPrefersConfigured(t *testing.T) {
	want := t.TempDir()
	f, _ := newTestFeature(t, map[string]any{optSaveDir: want})
	if got := f.saveDir(); got != want {
		t.Fatalf("saveDir = %q, 期望 %q", got, want)
	}
}

// TestSaveDirFallsBackToDataDir when no directory is configured.
func TestSaveDirFallsBackToDataDir(t *testing.T) {
	f, ctx := newTestFeature(t, map[string]any{optSaveDir: "  "})
	if got := f.saveDir(); got != ctx.DataDir {
		t.Fatalf("saveDir = %q, 期望 DataDir %q", got, ctx.DataDir)
	}
}

// TestWriteImageEncodesBothFormats writes a real image and checks the magic
// bytes, proving the format option actually reaches the encoder.
func TestWriteImageEncodesBothFormats(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	for _, tc := range []struct{ format, magic string }{
		{"png", "\x89PNG"},
		{"jpg", "\xff\xd8\xff"},
	} {
		f, ctx := newTestFeature(t, map[string]any{optFormat: tc.format})
		path := filepath.Join(ctx.DataDir, "shot"+f.ext())
		if err := f.writeImage(path, img); err != nil {
			t.Fatalf("%s 编码失败: %v", tc.format, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if len(data) < len(tc.magic) || string(data[:len(tc.magic)]) != tc.magic {
			t.Fatalf("%s 文件头 = %q, 期望 %q", tc.format, data[:len(tc.magic)], tc.magic)
		}
	}
}

// TestRememberPrunesPastLimit keeps only the newest max_history entries and
// deletes the overflow files from disk.
func TestRememberPrunesPastLimit(t *testing.T) {
	dir := t.TempDir()
	f, ctx := newTestFeature(t, map[string]any{optMaxHistory: 2})
	ctx.DataDir = dir

	var files []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, filepath.Base(t.Name())+string(rune('a'+i))+".png")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		files = append(files, p)
		f.remember(p)
	}

	f.mu.Lock()
	kept := append([]string(nil), f.saved...)
	f.mu.Unlock()

	if len(kept) != 2 {
		t.Fatalf("保留 %d 项, 期望 2 (%v)", len(kept), kept)
	}
	if kept[0] != files[2] || kept[1] != files[3] {
		t.Fatalf("应保留最新两项, 实际 %v", kept)
	}
	// The two oldest must be removed from disk.
	for _, old := range files[:2] {
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("旧文件应被删除: %s", old)
		}
	}
}

// TestRememberZeroLimitDisablesPruning: max <= 0 means "keep everything".
func TestRememberZeroLimitDisablesPruning(t *testing.T) {
	dir := t.TempDir()
	f, ctx := newTestFeature(t, map[string]any{optMaxHistory: 0})
	ctx.DataDir = dir

	p := filepath.Join(dir, "only.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	f.remember(p)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("limit=0 时不应删除: %v", err)
	}
}

// TestMaxHistoryToleratesTypes covers int/float shapes coming from YAML or JSON.
func TestMaxHistoryToleratesTypes(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{nil, defaultMaxHistory},
		{7, 7},
		{float64(9), 9},
		{0, defaultMaxHistory},
		{"x", defaultMaxHistory},
	}
	for _, c := range cases {
		opts := map[string]any{}
		if c.in != nil {
			opts[optMaxHistory] = c.in
		}
		_, ctx := newTestFeature(t, opts)
		if got := maxHistory(ctx); got != c.want {
			t.Errorf("maxHistory(%v) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestStateReportsRuntimeStatus mirrors what the panel reads back.
func TestStateReportsRuntimeStatus(t *testing.T) {
	f, ctx := newTestFeature(t, map[string]any{optFormat: "jpg", optMaxHistory: 5})
	f.running = true
	st := f.State()
	if st["running"] != true {
		t.Errorf("running = %v, 期望 true", st["running"])
	}
	if st["format"] != "jpg" {
		t.Errorf("format = %v, 期望 jpg", st["format"])
	}
	if st["max_history"] != 5 {
		t.Errorf("max_history = %v, 期望 5", st["max_history"])
	}
	if st["save_dir"] != ctx.DataDir {
		t.Errorf("save_dir = %v, 期望 %v", st["save_dir"], ctx.DataDir)
	}
}

// TestOptionsExposeConfigKeys keeps the panel form in sync with option keys.
func TestOptionsExposeConfigKeys(t *testing.T) {
	f := &Feature{}
	want := map[string]bool{optFormat: false, optJPGQuality: false, optCopyAfter: false, optSaveDir: false, optMaxHistory: false}
	for _, o := range f.Options() {
		if _, ok := want[o.Key]; ok {
			want[o.Key] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("模块未暴露配置项: %s", k)
		}
	}
}

// TestActionsExposeCaptureAndOpenDir ensures the panel buttons exist.
func TestActionsExposeCaptureAndOpenDir(t *testing.T) {
	f := &Feature{}
	ids := map[string]bool{}
	for _, a := range f.Actions() {
		ids[a.ID] = true
	}
	for _, want := range []string{actionCapture, actionOpenDir} {
		if !ids[want] {
			t.Errorf("缺少操作: %s", want)
		}
	}
}
