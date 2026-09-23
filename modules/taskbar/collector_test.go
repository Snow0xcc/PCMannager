package taskbar

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// testInterval 是采样测试使用的刷新间隔：足够短让测试跑得快，又不至于把
// CPU 打满（模块的真实默认值是 1s）。
const testInterval = 300 * time.Millisecond

// newTestCollector 构建一个带应用上下文的采集器，测试结束后自动 Stop。
func newTestCollector(t *testing.T, interval time.Duration) *Collector {
	t.Helper()
	ctx := &core.Context{Ctx: t.Context(), Logger: testLogger()}
	c := NewCollector(ctx, interval)
	t.Cleanup(c.Stop)
	return c
}

// waitStats 阻塞等待下一个采样快照，超时返回 false。
func waitStats(t *testing.T, c *Collector, timeout time.Duration) (Stats, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case s := <-c.Channel():
			return s, true
		case <-deadline:
			return Stats{}, false
		}
	}
}

// TestHumanScaleBoundaries 守护字节数 -> 二进制前缀文本的换算边界：
// 不足 1K 不带单位（单位由调用方自行拼接 B/s 或 b/s）、K 档不带小数、
// M/G 档保留一位小数，且最高只到 G 档。
func TestHumanScaleBoundaries(t *testing.T) {
	const (
		kb = 1024.0
		mb = 1024 * kb
		gb = 1024 * mb
	)
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"零值", 0, "0"},
		{"1 字节", 1, "1"},
		{"512 字节", 512, "512"},
		{"999 字节仍在字节档", 999, "999"},
		{"1023 字节仍在字节档", 1023, "1023"},
		{"正好 1K", kb, "1K"},
		{"1.5K 四舍五入到整 K", 1.5 * kb, "2K"},
		{"100K 不带小数", 100 * kb, "100K"},
		{"999K 仍留在 K 档", 999 * kb, "999K"},
		{"正好 1M", mb, "1.0M"},
		{"1.5M 保留一位小数", 1.5 * mb, "1.5M"},
		{"999M 仍留在 M 档", 999 * mb, "999.0M"},
		{"正好 1G", gb, "1.0G"},
		{"3.4G", 3.4 * gb, "3.4G"},
		{"超过 1T 仍用 G 档表示", 1024 * gb, "1024.0G"},
		{"负值原样输出", -5, "-5"},
	}
	for _, c := range cases {
		if got := humanScale(c.in); got != c.want {
			t.Errorf("humanScale(%v) = %q, 期望 %q（%s）", c.in, got, c.want, c.name)
		}
	}

	// 单位后缀的档位切换必须发生在准确的位置上，不能提前也不能延后。
	if got, want := humanScale(kb-1), "1023"; got != want {
		t.Errorf("humanScale(1023) = %q, 期望 %q（1023 不应进位到 K）", got, want)
	}
	if got, want := humanScale(mb-1), "1024K"; got != want {
		t.Errorf("humanScale(1M-1) = %q, 期望 %q（不足 1M 应留在 K 档）", got, want)
	}
	if got, want := humanScale(gb-1), "1024.0M"; got != want {
		t.Errorf("humanScale(1G-1) = %q, 期望 %q（不足 1G 应留在 M 档）", got, want)
	}

	// 不足 1K 的输出不能带任何单位后缀，否则调用方拼上 B/s 会得到 "512KB/s"。
	for _, v := range []float64{0, 1, 512, 999, 1023} {
		s := humanScale(v)
		if len(s) > 0 {
			switch s[len(s)-1] {
			case 'K', 'M', 'G':
				t.Errorf("humanScale(%v) = %q, 不足 1K 不应带单位后缀", v, s)
			}
		}
	}
}

// TestNewCollectorDefaultInterval 守护 NewCollector 的兜底行为：非正的
// interval 一律纠正成 1s，正常值原样保留，且 Channel 必须可用。
func TestNewCollectorDefaultInterval(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"零值兜底为 1s", 0, time.Second},
		{"负值兜底为 1s", -time.Second, time.Second},
		{"正常值原样保留", 1500 * time.Millisecond, 1500 * time.Millisecond},
	}
	for _, c := range cases {
		col := NewCollector(nil, c.in)
		if got := col.Interval(); got != c.want {
			t.Errorf("NewCollector(%v).Interval() = %v, 期望 %v（%s）", c.in, got, c.want, c.name)
		}
		if col.Channel() == nil {
			t.Errorf("NewCollector(%v).Channel() 不应为 nil（%s）", c.in, c.name)
		}
	}
}

// TestCollectorSetIntervalClamps 守护 SetInterval：非正值一律纠正成 1s，
// 正值即时生效（采样间隔的 [200ms,10s] 业务钳制是 Feature.interval 的职责）。
func TestCollectorSetIntervalClamps(t *testing.T) {
	col := newTestCollector(t, time.Second)

	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"零值纠正为 1s", 0, time.Second},
		{"负值纠正为 1s", -time.Hour, time.Second},
		{"正值原样生效", 2 * time.Second, 2 * time.Second},
		{"毫秒级原样生效", 250 * time.Millisecond, 250 * time.Millisecond},
	}
	for _, c := range cases {
		col.SetInterval(c.in)
		if got := col.Interval(); got != c.want {
			t.Errorf("SetInterval(%v) 后 Interval() = %v, 期望 %v（%s）", c.in, got, c.want, c.name)
		}
	}
}

// TestCollectorRunPublishesStats 守护后台采样：Run 起来之后 Channel 能持续
// 收到真实快照，且快照字段都落在合法区间内。
func TestCollectorRunPublishesStats(t *testing.T) {
	col := newTestCollector(t, testInterval)
	go col.Run()

	first, ok := waitStats(t, col, 15*time.Second)
	if !ok {
		t.Fatalf("等待 15s 后仍未收到首个采样快照")
	}
	assertSaneStats(t, "首个快照", first)

	second, ok := waitStats(t, col, 15*time.Second)
	if !ok {
		t.Fatalf("等待 15s 后仍未收到第二个采样快照（采样循环疑似停止）")
	}
	assertSaneStats(t, "第二个快照", second)
}

// TestCollectorStopEndsSampling 守护 Stop：采样 goroutine 必须退出、停止后
// 不再推送新快照、重复 Stop 不能 panic，且不留残余 goroutine。
func TestCollectorStopEndsSampling(t *testing.T) {
	baseline := runtime.NumGoroutine()

	col := newTestCollector(t, testInterval)
	done := make(chan struct{})
	go func() {
		col.Run()
		close(done)
	}()

	if _, ok := waitStats(t, col, 15*time.Second); !ok {
		t.Fatalf("Stop 之前应当至少收到一个采样快照")
	}

	col.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Stop() 之后 Run() 在 5s 内没有返回（采样 goroutine 未停止）")
	}

	// 停止后不允许再产生新快照：清空缓冲，再等两轮采样确认没有新数据。
	drain(col)
	time.Sleep(2 * testInterval)
	if got, ok := waitStats(t, col, 500*time.Millisecond); ok {
		t.Errorf("Stop() 之后仍在推送快照: %+v", got)
	}

	// 重复 Stop 必须安全（stopOnce 保证 done channel 只关闭一次）。
	col.Stop()
	col.Stop()

	// goroutine 泄漏检查：允许少量抖动，但基线不能被长期抬高。
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= baseline+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("疑似 goroutine 泄漏：基线 %d 个，Stop 后仍有 %d 个", baseline, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestCollectorStopsOnContextCancel 守护应用退出路径：ctx 被取消时 Run 自行
// 返回，无需显式调用 Stop。
func TestCollectorStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	col := NewCollector(&core.Context{Ctx: ctx, Logger: testLogger()}, testInterval)
	defer col.Stop()

	done := make(chan struct{})
	go func() {
		col.Run()
		close(done)
	}()

	if _, ok := waitStats(t, col, 15*time.Second); !ok {
		t.Fatalf("取消 ctx 之前应当至少收到一个采样快照")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("ctx 取消后 Run() 在 5s 内没有返回")
	}
}

// TestCollectorDropsSamplesWhenNoReader 守护背压策略：缓冲写满时采样循环
// 丢弃快照而不是阻塞，因此即使无人读取，Stop 也能让它立刻退出。
func TestCollectorDropsSamplesWhenNoReader(t *testing.T) {
	col := newTestCollector(t, testInterval)

	// 先把缓冲（容量 4）填满，让 Run 的发送必然走到 default 丢弃分支。
	filled := 0
	for filled < cap(col.ch) {
		select {
		case col.ch <- Stats{}:
			filled++
		default:
			t.Fatalf("预填充缓冲失败：只填入 %d/%d 条", filled, cap(col.ch))
		}
	}

	done := make(chan struct{})
	go func() {
		col.Run()
		close(done)
	}()

	// 等两轮采样过去，确认它确实尝试过向已满的 channel 发送。
	time.Sleep(2 * testInterval)

	col.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("无人读取时 Run() 卡在了发送上（应当丢弃快照而不是阻塞）")
	}
}

// TestDiskUsagePercentOrError 守护磁盘占用读取：要么返回 [0,100] 的百分比，
// 要么返回错误（平台上不支持时跳过）。
func TestDiskUsagePercentOrError(t *testing.T) {
	used, err := diskUsage()
	if err != nil {
		t.Skipf("当前平台（%s）不支持读取磁盘占用，跳过: %v", runtime.GOOS, err)
	}
	if used < 0 || used > 100 {
		t.Fatalf("diskUsage() = %v, 期望落在 [0,100] 区间内", used)
	}
}

// drain 排空 channel 里残留的旧快照。
func drain(c *Collector) {
	for {
		select {
		case <-c.Channel():
		default:
			return
		}
	}
}

// assertSaneStats 校验一份采样快照的字段都落在合法区间内。
func assertSaneStats(t *testing.T, label string, s Stats) {
	t.Helper()
	if s.RAM < 0 || s.RAM > 100 {
		t.Errorf("%s: RAM = %v, 期望落在 [0,100]", label, s.RAM)
	}
	if s.Disk < 0 || s.Disk > 100 {
		t.Errorf("%s: Disk = %v, 期望落在 [0,100]", label, s.Disk)
	}
	if s.CPU < 0 || s.CPU > 100 {
		t.Errorf("%s: CPU = %v, 期望落在 [0,100]", label, s.CPU)
	}
	if s.NetUp < 0 || s.NetDn < 0 {
		t.Errorf("%s: 网络速率不应为负: NetUp=%v NetDn=%v", label, s.NetUp, s.NetDn)
	}
	if s.Uptime < 0 {
		t.Errorf("%s: Uptime = %v, 期望非负", label, s.Uptime)
	}
}
