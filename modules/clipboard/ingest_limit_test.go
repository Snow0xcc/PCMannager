package clipboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"log/slog"
	"strings"
	"testing"
	"time"

	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// stubConfig 满足 core.ModuleConfig 的最小配置桩。
type ingestStubConfig struct{ vals map[string]any }

func (s *ingestStubConfig) Enabled() bool  { return true }
func (s *ingestStubConfig) Hotkey() string { return "" }
func (s *ingestStubConfig) Get(key string, def any) any {
	if v, ok := s.vals[key]; ok {
		return v
	}
	return def
}
func (s *ingestStubConfig) Set(string, any) error  { return nil }
func (s *ingestStubConfig) SetEnabled(bool) error  { return nil }
func (s *ingestStubConfig) SetHotkey(string) error { return nil }

// newIngestFeature 构造带日志捕获的 Feature，专测 ingest 路径。
func newIngestFeature(t *testing.T, vals map[string]any) (*Feature, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := NewFeature().(*Feature)
	f.ctx = &core.Context{
		Ctx:     context.Background(),
		Config:  &ingestStubConfig{vals: vals},
		Bus:     core.NewBus(),
		Logger:  logger,
		DataDir: t.TempDir(),
	}
	return f, buf
}

// TestIngestRejectsOversizedImage（B3）：单条图片超过 max_image_bytes 必须
// 拒收——不入历史、warn 上报。此前 ingest 无字节上限，5000 条 × 任意大 PNG
// 可直接 OOM。
func TestIngestRejectsOversizedImage(t *testing.T) {
	f, logs := newIngestFeature(t, map[string]any{
		"store_images":    true,
		"max_image_bytes": 1024,
	})
	big := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 400) // 1600 > 1024
	f.ingest(f.snapshot(), clip.Data{Format: clip.FmtImage, Bytes: big})

	if got := len(f.hist.All()); got != 0 {
		t.Fatalf("超限图片被入库：历史 %d 条，期望 0", got)
	}
	if !strings.Contains(logs.String(), "拒收") && !strings.Contains(logs.String(), "warn") {
		t.Errorf("拒收未产生 warn 日志，日志为: %q", logs.String())
	}
}

// TestIngestAcceptsImageWithinLimit：上限内的图片照常入库。
func TestIngestAcceptsImageWithinLimit(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{
		"store_images":    true,
		"max_image_bytes": 8 * 1024 * 1024,
	})
	f.ingest(f.snapshot(), clip.Data{Format: clip.FmtImage, Bytes: bytes.Repeat([]byte{'x'}, 4096)})
	if got := len(f.hist.All()); got != 1 {
		t.Fatalf("上限内图片未入库：历史 %d 条，期望 1", got)
	}
}

// TestIngestUnlimitedWhenCapNonPositive：max_image_bytes ≤ 0 语义为不限制
// （面板 Min 64KiB 只是防误触；手改配置 0/负数 = 关闭上限）。
func TestIngestUnlimitedWhenCapNonPositive(t *testing.T) {
	for _, cap := range []int{0, -1} {
		f, _ := newIngestFeature(t, map[string]any{
			"store_images":    true,
			"max_image_bytes": cap,
		})
		f.ingest(f.snapshot(), clip.Data{Format: clip.FmtImage, Bytes: bytes.Repeat([]byte{'y'}, 4096)})
		if got := len(f.hist.All()); got != 1 {
			t.Fatalf("cap=%d 应不限制：历史 %d 条，期望 1", cap, got)
		}
	}
}

// TestEchoSuppressionUsesHash（B3 附带）：写回回声抑制改为 32 字节哈希后
// 行为不变——同负载命中、不同负载放行，且不再持有整份图片副本。
func TestEchoSuppressionUsesHash(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{"store_images": true})
	img := []byte("fake-png-payload")
	f.mu.Lock()
	f.echoAt = time.Now()
	f.echoImageHash = sha256.Sum256(img)
	f.mu.Unlock()

	if !f.isEcho(clip.FmtImage, img) {
		t.Fatal("同负载应判为回声")
	}
	if f.isEcho(clip.FmtImage, []byte("different")) {
		t.Fatal("不同负载不应判为回声")
	}
}

// TestStateExposesMaxImageBytes：面板应能读到当前上限（配置回显一致性）。
func TestStateExposesMaxImageBytes(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{
		"store_images":    true,
		"max_image_bytes": 1024,
	})
	st := f.State()
	if v, ok := st["max_image_bytes"].(int); !ok || v != 1024 {
		t.Fatalf("State[max_image_bytes] = %v(%T), 期望 1024", st["max_image_bytes"], st["max_image_bytes"])
	}
}
