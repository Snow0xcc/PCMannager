package tray

import (
	"bytes"
	"image/png"
	"testing"
)

// TestEmbeddedIconAssetParses（TODO #9）：内嵌品牌图标必须是 16/32/48/64/256
// 五档 PNG-in-ICO，每一帧都能按声明尺寸解码，且 32px 帧确实画出了可见图案
// （防止生成器产出空白或截断资产而静默降级到 shell 图标）。
func TestEmbeddedIconAssetParses(t *testing.T) {
	entries, err := parseICO(embeddedIconICO)
	if err != nil {
		t.Fatalf("解析内嵌 icon.ico 失败: %v", err)
	}
	want := []int{16, 32, 48, 64, 256}
	if len(entries) != len(want) {
		t.Fatalf("帧数 = %d, 期望 %d", len(entries), len(want))
	}
	for i, w := range want {
		e := entries[i]
		if e.width != w || e.height != w {
			t.Errorf("entries[%d] 尺寸 = %dx%d, 期望 %dx%d", i, e.width, e.height, w, w)
		}
		if len(e.payload) < 8 || !bytes.HasPrefix(e.payload, []byte{0x89, 'P', 'N', 'G'}) {
			t.Errorf("entries[%d] 不是 PNG 载荷", i)
			continue
		}
		img, err := png.Decode(bytes.NewReader(e.payload))
		if err != nil {
			t.Errorf("entries[%d] PNG 解码失败: %v", i, err)
			continue
		}
		if b := img.Bounds(); b.Dx() != w || b.Dy() != w {
			t.Errorf("entries[%d] 解码尺寸 = %v, 期望 %dx%d", i, b, w, w)
		}
	}

	// 32px 帧必须有可见像素（alpha > 0）。
	img, err := embeddedIconRGBA(32)
	if err != nil {
		t.Fatalf("embeddedIconRGBA(32): %v", err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("embeddedIconRGBA(32) 尺寸 = %v", b)
	}
	visible := 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if img.RGBAAt(x, y).A > 0 {
				visible++
			}
		}
	}
	if visible < 50 {
		t.Errorf("32px 可见像素 = %d, 少于 50 —— 图标可能是空白的", visible)
	}
}

// TestPickICOEntry：目标尺寸 → 帧选择规则（最小的 ≥ target，否则最大的）。
func TestPickICOEntry(t *testing.T) {
	entries := []icoEntry{
		{width: 16, height: 16},
		{width: 32, height: 32},
		{width: 48, height: 48},
		{width: 256, height: 256},
	}
	for _, tc := range []struct {
		target, want int
	}{
		{8, 16},    // 小于所有帧 → 取最小
		{16, 16},   // 恰好命中
		{40, 48},   // 向上取最近的更大帧
		{100, 256}, // 中段 → 更大帧
		{512, 256}, // 超过最大 → 取最大
	} {
		got := pickICOEntry(entries, tc.target)
		if entries[got].width != tc.want {
			t.Errorf("pickICOEntry(target=%d) = %dpx, 期望 %dpx", tc.target, entries[got].width, tc.want)
		}
	}
}

// TestParseICORejectsGarbage：畸形容器必须报错而不是 panic 或越界读。
func TestParseICORejectsGarbage(t *testing.T) {
	for name, data := range map[string][]byte{
		"空":       {},
		"截断头":     {0, 0, 1, 0},
		"类型非图标":   {0, 0, 2, 0, 1, 0},
		"零帧":      {0, 0, 1, 0, 0, 0},
		"条目越界":    {0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 200, 0, 0, 0, 99, 0, 0, 0},
		"载荷越过文件尾": {0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 10, 0, 0, 0, 18, 0, 0, 0},
	} {
		if _, err := parseICO(data); err == nil {
			t.Errorf("%s: parseICO 应报错", name)
		}
	}
}

// TestResolveIconPriority：加载优先级 = 配置路径 → 内嵌 → 程序化品牌标 →
// shell 回退；空路径不得触发文件加载；一旦命中不得再调用后续步骤。
func TestResolveIconPriority(t *testing.T) {
	const (
		cfgH = uintptr(0x10)
		embH = uintptr(0x20)
		brH  = uintptr(0x30)
		shH  = uintptr(0x40)
	)
	// calls 记录步骤执行顺序，用于断言短路。
	var calls []string
	step := func(name string, h uintptr, fail bool) func(string) uintptr {
		return func(string) uintptr {
			calls = append(calls, name)
			if fail {
				return 0
			}
			return h
		}
	}

	// 1) 配置路径命中 → 只调用第一步。
	calls = nil
	got, src := resolveIcon("C:/icons/a.ico",
		step("config", cfgH, false), step("embedded", embH, false),
		step("brand", brH, false), step("shell", shH, false))
	if got != cfgH || src != iconSourceConfig {
		t.Errorf("配置命中: got=%#x source=%s", got, src)
	}
	if len(calls) != 1 || calls[0] != "config" {
		t.Errorf("calls = %v, 期望只 [config]", calls)
	}

	// 2) 配置路径加载失败 → 落到内嵌。
	calls = nil
	got, src = resolveIcon("C:/icons/bad.ico",
		step("config", cfgH, true), step("embedded", embH, false),
		step("brand", brH, false), step("shell", shH, false))
	if got != embH || src != iconSourceEmbedded {
		t.Errorf("配置失败回退: got=%#x source=%s", got, src)
	}
	if len(calls) != 2 {
		t.Errorf("calls = %v, 期望 [config embedded]", calls)
	}

	// 3) 空路径 → 不得调用文件加载。
	calls = nil
	got, src = resolveIcon("",
		step("config", cfgH, false), step("embedded", embH, false),
		step("brand", brH, false), step("shell", shH, false))
	if got != embH || src != iconSourceEmbedded {
		t.Errorf("空路径: got=%#x source=%s", got, src)
	}
	if len(calls) != 1 || calls[0] != "embedded" {
		t.Errorf("calls = %v, 期望只 [embedded]（空路径不碰文件系统）", calls)
	}

	// 4) 内嵌也失败 → 程序化品牌标。
	calls = nil
	got, src = resolveIcon("",
		step("config", cfgH, false), step("embedded", embH, true),
		step("brand", brH, false), step("shell", shH, false))
	if got != brH || src != iconSourceBrand {
		t.Errorf("品牌标回退: got=%#x source=%s", got, src)
	}

	// 5) 全部失败 → shell 兜底。
	calls = nil
	got, src = resolveIcon("",
		step("config", cfgH, true), step("embedded", embH, true),
		step("brand", brH, true), step("shell", shH, false))
	if got != shH || src != iconSourceShell {
		t.Errorf("shell 兜底: got=%#x source=%s", got, src)
	}
}
