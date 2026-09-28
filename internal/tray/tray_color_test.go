package tray

import "testing"

// TestColorKeyToCOLORREF 守护映射表的 BGR 字节序：RGB(255,165,0) 的 COLORREF
// 是 0x00A5FF（蓝|绿|红 从高到低），写反成 0x0000A5FF 的“字面橙”是常见错误。
func TestColorKeyToCOLORREF(t *testing.T) {
	cases := []struct {
		key   string
		want  uint32
		valid bool
	}{
		{"orange", 0x0000A5FF, true},
		{"black", 0x00000000, true},
		{"", 0, false},
		{"magenta", 0, false},
		{"Orange", 0, false}, // 键名区分大小写，未知即回退默认
	}
	for _, c := range cases {
		got, ok := colorKeyToCOLORREF(c.key)
		if ok != c.valid {
			t.Errorf("colorKeyToCOLORREF(%q) ok = %v, 期望 %v", c.key, ok, c.valid)
		}
		if ok && got != c.want {
			t.Errorf("colorKeyToCOLORREF(%q) = %#06x, 期望 %#06x", c.key, got, c.want)
		}
	}
}
