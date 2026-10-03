//go:build windows

package tray

import "testing"

// TestOwnerDrawPayloadRoundTrip 守护 owner-draw 的数据流：popupMenu 存入的
// {标题, 颜色} 必须能被 wndProc 用 dwItemData 查回，且菜单关闭后（clear），
// 同一个 key 不得再命中（避免复用旧菜单的残留条目）。
func TestOwnerDrawPayloadRoundTrip(t *testing.T) {
	tr := newStubTray()

	key1 := tr.putOwnerDraw("执行自动更新", 0x0000A5FF)
	key2 := tr.putOwnerDraw("另一项", 0x00000000)
	if key1 == key2 {
		t.Fatal("两个条目的 key 不应相同")
	}

	got1, ok := tr.getOwnerDraw(key1)
	if !ok || got1.title != "执行自动更新" || got1.color != 0x0000A5FF {
		t.Fatalf("key1 查回不符: ok=%v %+v", ok, got1)
	}
	got2, ok := tr.getOwnerDraw(key2)
	if !ok || got2.title != "另一项" || got2.color != 0x00000000 {
		t.Fatalf("key2 查回不符: ok=%v %+v", ok, got2)
	}

	tr.clearOwnerDraw()
	if _, ok := tr.getOwnerDraw(key1); ok {
		t.Fatal("clear 后 key1 不应再命中")
	}
}

// TestOdKeyWithFlags 验证禁用位（最高位）的叠加与剥离：绘制消息携带的
// itemData 带位，查表必须用裸 key。
func TestOdKeyWithFlags(t *testing.T) {
	key := uintptr(7)
	if got := odKeyWithFlags(key, false); got != key {
		t.Fatalf("未禁用不应改 key: got %d", got)
	}
	flagged := odKeyWithFlags(key, true)
	if flagged&odDisabledBit == 0 {
		t.Fatal("禁用 key 应含最高位")
	}
	if plain := flagged &^ odDisabledBit; plain != key {
		t.Fatalf("剥离禁用位后应还原: got %d want %d", plain, key)
	}

	// 与 payload 往返：带位的 key 剥离后查回同一内容。
	tr := newStubTray()
	stored := tr.putOwnerDraw("灰项", 0x0000A5FF)
	fk := odKeyWithFlags(stored, true)
	if it, ok := tr.getOwnerDraw(fk &^ odDisabledBit); !ok || it.title != "灰项" {
		t.Fatalf("带位 key 剥离后查回失败: ok=%v %+v", ok, it)
	}
}

// TestColorKeyToCOLORREFUnknownFallsBack 呼应 tray_color_test.go 的表级断言，
// 这里验证绘制路径使用的调用形态：未知键回退后不应进入 owner-draw 分支
// （popupMenu 用 ok 判断决定走 MF_STRING 还是 MF_OWNERDRAW）。
func TestColorKeyToCOLORREFUnknownFallsBack(t *testing.T) {
	if _, ok := colorKeyToCOLORREF("chartreuse"); ok {
		t.Fatal("未知色名不应声称有效")
	}
	if _, ok := colorKeyToCOLORREF(""); ok {
		t.Fatal("空色名不应声称有效（空 = 默认色，无需 owner-draw）")
	}
}
