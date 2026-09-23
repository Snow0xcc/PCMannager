package core

import "testing"

func TestParseHotkeyCanonicalizes(t *testing.T) {
	cases := []struct {
		in   string
		text string
	}{
		{"ctrl+alt+t", "ctrl+alt+t"},
		{"ALT+CTRL+T", "ctrl+alt+t"},       // order + case insensitive
		{"ctrl + alt + f1", "ctrl+alt+f1"}, // spaces tolerated
		{"Ctrl+`", "ctrl+`"},
		{"F1", "f1"},
		// Modifiers are re-emitted in canonical ctrl+alt+shift+win order,
		// regardless of the order the user typed them in.
		{"win+shift+s", "shift+win+s"},
		{"ctrl+alt+shift+win+a", "ctrl+alt+shift+win+a"},
	}
	for _, tc := range cases {
		combo, ok, err := ParseHotkey(tc.in)
		if err != nil || !ok {
			t.Fatalf("ParseHotkey(%q) = err %v, ok %v", tc.in, err, ok)
		}
		if combo.Text != tc.text {
			t.Fatalf("ParseHotkey(%q).Text = %q, 期望 %q", tc.in, combo.Text, tc.text)
		}
	}
}

func TestParseHotkeyEmptyIsUnbound(t *testing.T) {
	combo, ok, err := ParseHotkey("")
	if err != nil {
		t.Fatalf("空热键不应报错: %v", err)
	}
	if ok {
		t.Fatal("空热键应表示未绑定 (ok=false)")
	}
	if combo != (Combo{}) {
		t.Fatal("空热键应返回零值 Combo")
	}
}

func TestParseHotkeyRejectsInvalid(t *testing.T) {
	bad := []string{
		"ctrl+alt",       // 缺少主键
		"ctrl+alt+t+u",   // 多个主键
		"ctrl+nosuchkey", // 未知按键
	}
	for _, s := range bad {
		if _, ok, err := ParseHotkey(s); err == nil {
			t.Fatalf("ParseHotkey(%q) 应报错", s)
		} else if ok {
			t.Fatalf("ParseHotkey(%q) 不应返回 ok", s)
		}
	}
}

func TestParseHotkeyModifierBits(t *testing.T) {
	combo, _, err := ParseHotkey("ctrl+alt+shift+win+k")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if combo.Mods != ModCtrl|ModAlt|ModShift|ModWin {
		t.Fatalf("Mods = %#x, 期望 %#x", combo.Mods, ModCtrl|ModAlt|ModShift|ModWin)
	}
	if combo.VK != 0x4B { // 'K'
		t.Fatalf("VK = %#x, 期望 0x4B", combo.VK)
	}
}

func TestValidHotkey(t *testing.T) {
	good := []string{"", "ctrl+alt+t", "f1", "win+shift+s", "ctrl+`"}
	for _, s := range good {
		if !ValidHotkey(s) {
			t.Fatalf("ValidHotkey(%q) 应为 true", s)
		}
	}
	bad := []string{"ctrl+alt", "ctrl+bogus", "ctrl+a+b"}
	for _, s := range bad {
		if ValidHotkey(s) {
			t.Fatalf("ValidHotkey(%q) 应为 false", s)
		}
	}
}

// TestHotkeyManagerBindInvalidWarns checks an invalid hotkey is reported
// through the warn hook instead of panicking or silently binding.
func TestHotkeyManagerBindInvalidWarns(t *testing.T) {
	var warned bool
	h := NewHotkeyManager(func(string, ...any) { warned = true })
	defer h.Stop()

	h.Bind("taskbar", "ctrl+alt", func() error { return nil })
	if !warned {
		t.Fatal("无效热键应触发告警")
	}
	if c, ok := h.Combo("taskbar"); ok {
		t.Fatalf("无效热键不应绑定: %+v", c)
	}
}

// TestHotkeyManagerEmptyUnbinds makes sure "" means "no hotkey" and clears
// any previous binding without erroring.
func TestHotkeyManagerEmptyUnbinds(t *testing.T) {
	h := NewHotkeyManager(nil)
	defer h.Stop()

	h.Bind("taskbar", "", func() error { return nil })
	if _, ok := h.Combo("taskbar"); ok {
		t.Fatal("空热键不应产生绑定")
	}

	h.Unbind("taskbar")
	if _, ok := h.Combo("taskbar"); ok {
		t.Fatal("Unbind 后不应存在绑定")
	}
}

func TestHotkeyManagerStopIsIdempotent(t *testing.T) {
	h := NewHotkeyManager(nil)
	h.Bind("taskbar", "ctrl+alt+t", func() error { return nil })
	h.Stop()
	h.Stop() // must not panic or double-close the backend
}

func TestHotkeyManagerConflictsEmptyWhenDistinct(t *testing.T) {
	h := NewHotkeyManager(nil)
	defer h.Stop()

	// Distinct combos can never conflict, even if registration is unsupported
	// on this platform (which is the case off Windows).
	h.Bind("a", "ctrl+alt+t", func() error { return nil })
	h.Bind("b", "ctrl+alt+y", func() error { return nil })
	if got := h.Conflicts(); len(got) != 0 {
		t.Fatalf("不应存在冲突: %v", got)
	}
}
