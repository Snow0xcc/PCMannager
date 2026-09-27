//go:build windows

package taskbar

import "testing"

// TestGridMetricsScaleWithFont 守护网格字段随字号增长。
//
// 这是“字体调大后排版崩掉”的回归测试：旧布局把数值域、单位域写成固定像素常量，
// 字号一变就与文本不匹配，表现为文字挤压/重叠。字段宽度必须由实测文本尺寸推导。
func TestGridMetricsScaleWithFont(t *testing.T) {
	// 小字（约 9pt 的量级）与大字（约 18pt）的实测尺寸。
	small := computeGridMetrics(14, 30, 34, 12, 26, 28)
	large := computeGridMetrics(28, 60, 68, 24, 52, 56)

	if large.rowH <= small.rowH {
		t.Errorf("行高未随字号增长: 大 %d, 小 %d", large.rowH, small.rowH)
	}
	if large.numFieldW <= small.numFieldW {
		t.Errorf("数值域未随字号增长: 大 %d, 小 %d", large.numFieldW, small.numFieldW)
	}
	if large.labelFieldW <= small.labelFieldW {
		t.Errorf("标签域未随字号增长: 大 %d, 小 %d", large.labelFieldW, small.labelFieldW)
	}
	if large.blockH <= small.blockH {
		t.Errorf("两行块高未随字号增长: 大 %d, 小 %d", large.blockH, small.blockH)
	}

	// 两行块必须由行高推出，否则大字号下两行会重叠。
	if small.blockH != 2*small.rowH || large.blockH != 2*large.rowH {
		t.Errorf("blockH 应为 2*rowH: small=%d/%d large=%d/%d",
			small.blockH, small.rowH, large.blockH, large.rowH)
	}
}

// TestGridMetricsFieldsFitContent 守护字段不小于其内容，否则文本会被裁掉。
func TestGridMetricsFieldsFitContent(t *testing.T) {
	m := computeGridMetrics(14, 30, 34, 12, 26, 28)

	if m.labelFieldW < 26 {
		t.Errorf("标签域 %d 小于标签实测宽度 26", m.labelFieldW)
	}
	if m.sysValueW < 30 {
		t.Errorf("系统数值域 %d 小于 \"100%%\" 实测宽度 30", m.sysValueW)
	}
	if m.numFieldW < 34 {
		t.Errorf("数值域 %d 小于四位数字实测宽度 34", m.numFieldW)
	}
	if m.unitW < 28 {
		t.Errorf("单位域 %d 小于 \"KB/s\" 实测宽度 28", m.unitW)
	}
	if m.labelGap <= 0 || m.colGap <= 0 || m.unitGap <= 0 {
		t.Errorf("间距应为正: label=%d col=%d unit=%d", m.labelGap, m.colGap, m.unitGap)
	}
}

// TestGridMetricsBatteryGlyphFitsRow 守护电池图标不高于行高。
//
// 图标高于行内容会侵入相邻行，正是“两行挤在一起”的观感来源之一。
func TestGridMetricsBatteryGlyphFitsRow(t *testing.T) {
	for _, lineH := range []int32{8, 12, 14, 20, 28, 40} {
		m := computeGridMetrics(lineH, 30, 34, lineH-2, 26, 28)
		if m.batteryH <= 0 || m.batteryW <= 0 {
			t.Fatalf("行高 %d: 电池图标尺寸无效 %dx%d", lineH, m.batteryW, m.batteryH)
		}
		// 行高含 leading，图标必须能容纳在内。
		if m.batteryH > m.rowH {
			t.Errorf("行高 %d: 电池高 %d 超过行高 %d", lineH, m.batteryH, m.rowH)
		}
	}
}

// TestGridMetricsHandlesZeroMeasurements 守护测量失败（返回 0）时仍给出可用尺寸，
// 否则在无字体/无 DC 的边缘情况下会画出 0 宽域而完全看不到内容。
func TestGridMetricsHandlesZeroMeasurements(t *testing.T) {
	m := computeGridMetrics(0, 0, 0, 0, 0, 0)
	if m.rowH < 8 {
		t.Errorf("零测量时行高 %d 过小", m.rowH)
	}
	if m.labelFieldW <= 0 || m.numFieldW < 0 || m.unitW < 0 {
		t.Errorf("零测量时字段宽度无效: label=%d num=%d unit=%d",
			m.labelFieldW, m.numFieldW, m.unitW)
	}
	if m.colGap <= 0 || m.labelGap <= 0 {
		t.Errorf("零测量时间距应为正: col=%d label=%d", m.colGap, m.labelGap)
	}
}
