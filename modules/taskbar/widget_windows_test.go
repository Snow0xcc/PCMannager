//go:build windows

package taskbar

import (
	"strings"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/winui"
)

// TestPartsIncludeBatteryOnlyWhenPresent 守护电量字段：没有电池的设备不应
// 出现 BAT 读数（否则面板会显示误导性的 0%），开启开关且有电池时才出现。
//
// 从 feature_test.go 移来：widget.parts 与 widget 的 font 字段都定义在
// widget_windows.go，非 Windows 侧没有对应实现；放在平台无关测试文件会让
// linux/darwin 的 go vet / go test 因 undefined 符号失败（CI 的 Linux runner
// 正是因此红掉）。
func TestPartsIncludeBatteryOnlyWhenPresent(t *testing.T) {
	f := newTestFeature(t, map[string]any{optShowBattery: true})

	w := &widget{feat: f}
	got := w.parts(Stats{BatteryPresent: false, BatteryPercent: 0})
	if strings.Contains(strings.Join(got, " "), "BAT") {
		t.Fatalf("无电池时不应出现 BAT 读数: %v", got)
	}

	got = w.parts(Stats{BatteryPresent: true, BatteryPercent: 73, BatteryCharging: true})
	if !containsPart(got, "BAT 73%+") {
		t.Fatalf("有电池且充电时应出现 BAT 73%%+: %v", got)
	}
	got = w.parts(Stats{BatteryPresent: true, BatteryPercent: 42})
	if !containsPart(got, "BAT 42%") {
		t.Fatalf("有电池未充电时应出现 BAT 42%%: %v", got)
	}
}

// TestPartsOmitBatteryWhenDisabled 守护开关：关闭 optShowBattery 时即使有
// 电池也不显示（同 TestPartsIncludeBatteryOnlyWhenPresent 的迁移说明）。
func TestPartsOmitBatteryWhenDisabled(t *testing.T) {
	f := newTestFeature(t, map[string]any{optShowBattery: false})
	w := &widget{feat: f}
	got := w.parts(Stats{BatteryPresent: true, BatteryPercent: 88})
	if containsPart(got, "BAT 88%") {
		t.Fatalf("关闭显示电量时不应出现 BAT: %v", got)
	}
}

// TestHorizontalPosAnchorsLeftOfTray 守护 TrafficMonitor 式定位：
// 组件必须紧贴通知区域（时钟）左侧，而不是贴任务栏右边缘——后者会压在时钟上。
func TestHorizontalPosAnchorsLeftOfTray(t *testing.T) {
	f := newTestFeature(t, nil)
	w := &widget{feat: f}

	m := winui.TaskbarMetrics{
		Width:       1920,
		NotifyLeft:  1400, // 通知区域从 1400 开始
		NotifyFound: true,
	}

	const widgetWidth = 200
	x := w.horizontalPos(m, widgetWidth)

	// 默认 offset_x=8 且保留 2px 间隙：x = 1400 - 2 - 200 - 8 = 1190。
	if x != 1190 {
		t.Fatalf("horizontalPos() = %d, 期望 1190（通知区左侧）", x)
	}

	// 关键性质：右边缘绝不能越过通知区左边界，否则会压住时钟/托盘图标。
	if right := x + widgetWidth; right > m.NotifyLeft {
		t.Fatalf("组件右边缘 %d 越过了通知区域左边界 %d，会压住时钟", right, m.NotifyLeft)
	}
}

// TestHorizontalPosLeavesGapBeforeTray 守护关键性质：组件右边缘必须严格小于
// 通知区左边界（而非相等），否则边框/圆角会与托盘产生可见重叠。
func TestHorizontalPosLeavesGapBeforeTray(t *testing.T) {
	// offset_x 设为 0，只验证基础间隙。
	f := newTestFeature(t, map[string]any{optOffsetX: 0})
	w := &widget{feat: f}

	m := winui.TaskbarMetrics{Width: 2240, NotifyLeft: 1745, NotifyFound: true}
	const width = 200
	x := w.horizontalPos(m, width)

	if right := x + width; right >= m.NotifyLeft {
		t.Fatalf("组件右边缘 %d 应严格小于通知区左边界 %d（需要间隙）", right, m.NotifyLeft)
	}
	if got, want := x+width, m.NotifyLeft-2; got != want {
		t.Fatalf("组件右边缘 = %d, 期望 %d（保留 2px 间隙）", got, want)
	}
}

// TestHorizontalPosFallsBackToRightEdge 守护找不到通知区域时的回退：
// 至少不能跑出任务栏左边界。
func TestHorizontalPosFallsBackToRightEdge(t *testing.T) {
	f := newTestFeature(t, map[string]any{optOffsetX: 10})
	w := &widget{feat: f}

	m := winui.TaskbarMetrics{Width: 1920, NotifyFound: false}
	x := w.horizontalPos(m, 200)
	if want := int32(1920 - 200 - 10); x != want {
		t.Fatalf("回退定位 = %d, 期望 %d（任务栏右边缘）", x, want)
	}

	// 任务栏比组件还窄时，钳制到 0 而不是负值。
	narrow := winui.TaskbarMetrics{Width: 100, NotifyFound: false}
	if got := w.horizontalPos(narrow, 200); got != 0 {
		t.Fatalf("窄任务栏定位 = %d, 期望钳制到 0", got)
	}
}

// TestHorizontalPosClampsNegative 守护通知区域过靠左时不产生负坐标。
func TestHorizontalPosClampsNegative(t *testing.T) {
	f := newTestFeature(t, nil)
	w := &widget{feat: f}

	// 通知区左边界 100，组件宽 200 => 计算结果为负，必须钳到 0。
	m := winui.TaskbarMetrics{Width: 1920, NotifyLeft: 100, NotifyFound: true}
	if got := w.horizontalPos(m, 200); got != 0 {
		t.Fatalf("horizontalPos() = %d, 期望钳制到 0", got)
	}
}

// TestWidthOptionAffectsLayout 守护可配置宽度。
func TestWidthOptionAffectsLayout(t *testing.T) {
	f := newTestFeature(t, map[string]any{optWidth: 320})
	w := &widget{feat: f}

	m := winui.TaskbarMetrics{Width: 1920, NotifyLeft: 1400, NotifyFound: true}
	const width = 320
	x := w.horizontalPos(m, width)

	if right := x + width; right > m.NotifyLeft {
		t.Fatalf("加宽后右边缘 %d 越过通知区边界 %d", right, m.NotifyLeft)
	}
}

// TestForegroundIsInverseOfTaskbar 守护文字取反色：
// 浅色任务栏 => 深色（黑）文字；深色任务栏 => 浅色（白）文字。
// 这正是“文字与任务栏背景相反更突出”的契约。
func TestForegroundIsInverseOfTaskbar(t *testing.T) {
	f := newTestFeature(t, nil)
	w := &widget{feat: f}

	cases := []struct {
		name     string
		sampled  uint32
		wantDark bool // true 表示期望深色文字
	}{
		{"纯白任务栏配黑字", winui.RGB(255, 255, 255), true},
		{"浅灰任务栏配黑字", winui.RGB(213, 219, 233), true},
		{"深色任务栏配白字", winui.RGB(32, 33, 36), false},
		{"纯黑任务栏配白字", winui.RGB(0, 0, 0), false},
	}
	for _, c := range cases {
		got := w.foreground(true, c.sampled)
		isDark := got == winui.RGB(0, 0, 0)
		if isDark != c.wantDark {
			t.Errorf("%s: foreground(0x%06X) = 0x%06X, 期望%s文字",
				c.name, c.sampled, got, map[bool]string{true: "深色", false: "浅色"}[c.wantDark])
		}
	}
}

// TestForegroundManualOverrideWins 守护手动颜色优先于自动取反。
func TestForegroundManualOverrideWins(t *testing.T) {
	f := newTestFeature(t, map[string]any{
		optAutoFG:  false,
		optFGColor: "#FF0000",
	})
	w := &widget{feat: f}

	if got, want := w.foreground(true, winui.RGB(255, 255, 255)), winui.RGB(255, 0, 0); got != want {
		t.Fatalf("关闭自动取色后应采用手动颜色: got=0x%06X want=0x%06X", got, want)
	}
}

// TestForegroundWithoutSampleFallsBackToSystemTheme 守护无采样时的兜底：
// 必须仍能给出可读颜色，而不是与背景同色。
func TestForegroundWithoutSampleFallsBackToSystemTheme(t *testing.T) {
	f := newTestFeature(t, nil)
	w := &widget{feat: f}

	got := w.foreground(false, 0)
	if got != winui.RGB(0, 0, 0) && got != winui.RGB(255, 255, 255) {
		t.Fatalf("无采样时应回退到黑白之一, 实际 0x%06X", got)
	}
}
