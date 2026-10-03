//go:build windows

package launcher

import "testing"

// TestPanelSize 守护单行布局几何：面板宽度固定为屏幕 3/5，高度恒定（搜索框
// + 一行磁贴），不随条目数增加而变高。
//
// 本文件带 //go:build windows：panelSize/gridTileW/gridPadY 等定义在
// panel_windows.go 里，随 Windows 构建才能解析；放在平台无关的测试文件会
// 让 linux/darwin 的 go vet / go test 因 undefined 符号失败。
func TestPanelSize(t *testing.T) {
	const screenW = int32(2240)
	w, h := panelSize(1, screenW)
	if w != screenW*3/5 {
		t.Fatalf("面板宽 %d 应为屏宽 3/5 (%d)", w, screenW*3/5)
	}
	if h != gridPadY*2+panelEditH+gridTileH {
		t.Fatalf("面板高 %d 应为恒定单行高 %d", h, gridPadY*2+panelEditH+gridTileH)
	}
	// 条目再多，宽高也不变（内容靠横向滚动）。
	w2, h2 := panelSize(20, screenW)
	if w2 != w || h2 != h {
		t.Fatalf("面板尺寸不应随条目数变化: (%d,%d) vs (%d,%d)", w2, h2, w, h)
	}
}

// TestContentWidth 守护内容总宽计算：N 个磁贴 + 间距 + 内边距。
func TestContentWidth(t *testing.T) {
	c1 := contentWidth(1)
	if c1 != gridPadX*2+gridTileW {
		t.Fatalf("contentWidth(1)=%d, 期望 %d", c1, gridPadX*2+gridTileW)
	}
	c3 := contentWidth(3)
	if c3 != gridPadX*2+3*gridTileW+2*gridGap {
		t.Fatalf("contentWidth(3)=%d, 期望 %d", c3, gridPadX*2+3*gridTileW+2*gridGap)
	}
}
