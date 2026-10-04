package updater

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// countingTransport 统计出站请求数，用于断言"禁用的模块不产生网络请求"。
type countingTransport struct{ n atomic.Int64 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return nil, context.Canceled
}

// newCountingFeature 构造带测试 seam 的 Feature：极短的首检延迟 + 计数 HTTP。
func newCountingFeature(t *testing.T, autoCheck bool) (*Feature, *countingTransport) {
	t.Helper()
	tr := &countingTransport{}
	f := &Feature{
		firstCheckDelay: 10 * time.Millisecond,
		checkInterval:   time.Hour,
		httpClientFn:    func() *http.Client { return &http.Client{Transport: tr} },
	}
	f.ctx = &core.Context{
		Ctx:     context.Background(),
		Config:  &stubConfig{vals: map[string]any{optAutoCheck: autoCheck}},
		App:     stubAppControl{version: "1.2.3"},
		DataDir: t.TempDir(),
		Bus:     core.NewBus(),
		Logger:  slog.New(slog.DiscardHandler),
	}
	return f, tr
}

// TestDisabledModuleMakesNoRequests（A4）：模块关闭时 app 不会调 Start，
// 旧实现却在 Init 里就启动轮询 goroutine（门控只有 auto_check，不看
// Enabled），导致"默认关闭"的模块 60 秒后照样出网。
func TestDisabledModuleMakesNoRequests(t *testing.T) {
	f, tr := newCountingFeature(t, true) // auto_check 开着也没用：模块未启用
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if got := tr.n.Load(); got != 0 {
		t.Fatalf("模块未启用（未 Start）却发出了 %d 次请求", got)
	}
}

// TestStartTriggersCheck：启用后 Start 应开启轮询并完成首检。
func TestStartTriggersCheck(t *testing.T) {
	f, tr := newCountingFeature(t, true)
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := f.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer f.Stop()
	time.Sleep(120 * time.Millisecond)
	if got := tr.n.Load(); got == 0 {
		t.Fatal("Start 后未产生任何检查请求")
	}
}

// TestStopJoinsLoop：Stop 后轮询必须终止（旧实现 goroutine 无人 join），
// 且 Stop 幂等、可重新 Start。
func TestStopJoinsLoop(t *testing.T) {
	f, tr := newCountingFeature(t, true)
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := f.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := f.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := f.Stop(); err != nil { // 幂等
		t.Fatalf("第二次 Stop: %v", err)
	}
	after := tr.n.Load()
	time.Sleep(80 * time.Millisecond)
	if got := tr.n.Load(); got != after {
		t.Fatalf("Stop 后仍产生请求：%d → %d", after, got)
	}
	// 重新 Start 应恢复轮询（新的 stop 通道）。
	if err := f.Start(); err != nil {
		t.Fatalf("重新 Start: %v", err)
	}
	defer f.Stop()
	time.Sleep(80 * time.Millisecond)
	if got := tr.n.Load(); got <= after {
		t.Fatalf("重新 Start 后未恢复检查：%d ≤ %d", got, after)
	}
}

// TestAutoCheckOffMakesNoRequests：auto_check=false 时即使启用也不轮询。
func TestAutoCheckOffMakesNoRequests(t *testing.T) {
	f, tr := newCountingFeature(t, false)
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := f.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer f.Stop()
	time.Sleep(80 * time.Millisecond)
	if got := tr.n.Load(); got != 0 {
		t.Fatalf("auto_check=false 仍发出 %d 次请求", got)
	}
}

// TestStartConcurrentWithStateReads（评审 I-1）：Start 写 f.last.Current 与
// 面板 5s 轮询 State() 读并发可达，写侧必须持锁。仅在 -race 下有效。
func TestStartConcurrentWithStateReads(t *testing.T) {
	f, _ := newCountingFeature(t, false) // auto_check=false：不起轮询，专注竞态
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = f.State()
		}
	}()
	for i := 0; i < 50; i++ {
		if err := f.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	<-done
}

// TestAutoCheckOptionRequiresRestart（评审 M-2）：轮询 goroutine 只在 Start
// 创建，auto_check 运行时改值必须经模块重启才生效，应声明 Restart: true，
// 由 ApplyOption 的既有机制承接。
func TestAutoCheckOptionRequiresRestart(t *testing.T) {
	for _, o := range (&Feature{}).Options() {
		if o.Key == optAutoCheck {
			if !o.Restart {
				t.Fatal("auto_check 应声明 Restart: true")
			}
			return
		}
	}
	t.Fatal("Options() 未声明 auto_check")
}
