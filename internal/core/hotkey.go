package core

import (
	"fmt"
	"strings"
	"sync"
)

// Modifier bits, mirroring the Windows MOD_* constants so a parsed value can
// be handed straight to RegisterHotKey.
const (
	ModAlt      uint32 = 0x0001
	ModCtrl     uint32 = 0x0002
	ModShift    uint32 = 0x0004
	ModWin      uint32 = 0x0008
	ModNoRepeat uint32 = 0x4000
)

// Combo is a parsed global hotkey: modifier bits plus a virtual key.
type Combo struct {
	Mods uint32
	VK   uint32
	// Text is the canonical normalized form, e.g. "ctrl+alt+t".
	Text string
}

// String renders the combo in canonical "ctrl+alt+t" form.
func (c Combo) String() string { return c.Text }

// keyNameToVK maps friendly key names to Windows virtual-key codes. The same
// table is used on every platform for parsing/validation, so a config written
// on Linux still means the same thing on Windows.
var keyNameToVK = map[string]uint32{
	// letters
	"a": 0x41, "b": 0x42, "c": 0x43, "d": 0x44, "e": 0x45, "f": 0x46,
	"g": 0x47, "h": 0x48, "i": 0x49, "j": 0x4A, "k": 0x4B, "l": 0x4C,
	"m": 0x4D, "n": 0x4E, "o": 0x4F, "p": 0x50, "q": 0x51, "r": 0x52,
	"s": 0x53, "t": 0x54, "u": 0x55, "v": 0x56, "w": 0x57, "x": 0x58,
	"y": 0x59, "z": 0x5A,
	// digits (top row)
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,
	// function keys
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74,
	"f6": 0x75, "f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79,
	"f11": 0x7A, "f12": 0x7B, "f13": 0x7C, "f14": 0x7D, "f15": 0x7E,
	"f16": 0x7F, "f17": 0x80, "f18": 0x81, "f19": 0x82, "f20": 0x83,
	// punctuation (OEM keys)
	"`": 0xC0, "-": 0xBD, "=": 0xBB, "[": 0xDB, "]": 0xDD, "\\": 0xDC,
	";": 0xBA, "'": 0xDE, ",": 0xBC, ".": 0xBE, "/": 0xBF,
	// navigation & editing
	"esc": 0x1B, "tab": 0x09, "space": 0x20,
	"enter": 0x0D, "backspace": 0x08, "delete": 0x2E,
	"insert": 0x2D, "home": 0x24, "end": 0x23,
	"pageup": 0x21, "pagedown": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"printscreen": 0x2C,
	// numpad
	"num0": 0x60, "num1": 0x61, "num2": 0x62, "num3": 0x63, "num4": 0x64,
	"num5": 0x65, "num6": 0x66, "num7": 0x67, "num8": 0x68, "num9": 0x69,
	"multiply": 0x6A, "add": 0x6B, "subtract": 0x6D, "decimal": 0x6E, "divide": 0x6F,
}

// vkToName is the reverse table used to canonicalize a parsed combo.
// Built once from the smallest spelling available for each VK.
var vkToName = func() map[uint32]string {
	m := make(map[uint32]string, len(keyNameToVK))
	for name, vk := range keyNameToVK {
		if prev, ok := m[vk]; !ok || len(name) < len(prev) {
			m[vk] = name
		}
	}
	return m
}()

// ParseHotkey converts "ctrl+alt+t" into a Combo.
//
// Accepted modifiers: ctrl/control, alt, shift, win/super/meta/cmd.
// Exactly one non-modifier key is required. An empty string is valid and means
// "no hotkey" (returns ok=false, err=nil).
func ParseHotkey(s string) (Combo, bool, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Combo{}, false, nil
	}
	lower := strings.ToLower(strings.ReplaceAll(raw, " ", ""))

	var (
		mods     uint32
		vk       uint32
		haveKey  bool
		modOrder []string
	)
	for _, part := range strings.Split(lower, "+") {
		part = strings.TrimSpace(part)
		switch part {
		case "":
			continue
		case "ctrl", "control":
			mods |= ModCtrl
			modOrder = append(modOrder, "ctrl")
		case "alt":
			mods |= ModAlt
			modOrder = append(modOrder, "alt")
		case "shift":
			mods |= ModShift
			modOrder = append(modOrder, "shift")
		case "win", "super", "meta", "cmd":
			mods |= ModWin
			modOrder = append(modOrder, "win")
		default:
			code, ok := keyNameToVK[part]
			if !ok {
				return Combo{}, false, fmt.Errorf("未知按键 %q", part)
			}
			if haveKey {
				return Combo{}, false, fmt.Errorf("热键只能包含一个主键: %q", raw)
			}
			vk, haveKey = code, true
		}
	}
	if !haveKey {
		return Combo{}, false, fmt.Errorf("热键缺少主键: %q", raw)
	}

	// Canonicalize modifier order as ctrl+alt+shift+win.
	var b strings.Builder
	for _, want := range []string{"ctrl", "alt", "shift", "win"} {
		for _, got := range modOrder {
			if got == want {
				b.WriteString(want)
				b.WriteByte('+')
				break
			}
		}
	}
	name, ok := vkToName[vk]
	if !ok {
		return Combo{}, false, fmt.Errorf("无法规范化按键: %q", raw)
	}
	b.WriteString(name)

	return Combo{Mods: mods, VK: vk, Text: b.String()}, true, nil
}

// ValidHotkey reports whether s parses to a usable hotkey ("" is valid).
func ValidHotkey(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	_, ok, err := ParseHotkey(s)
	return ok && err == nil
}

// hotkeyBackend is the platform-specific global hotkey implementation.
type hotkeyBackend interface {
	// register installs a hotkey with the OS and returns its handle id.
	register(id int, c Combo) error
	// unregister removes a previously registered hotkey.
	unregister(id int) error
	// run pumps platform messages, dispatching fired hotkey ids on ch.
	run(ch chan<- int)
	// close releases the backend and stops run.
	close()
}

// HotkeyManager owns OS-level hotkey registrations and routes presses to
// module handlers by module id.
type HotkeyManager struct {
	mu       sync.Mutex
	backend  hotkeyBackend
	byModule map[string]int // module id -> registration id
	bySlot   map[int]string // registration id -> module id
	combos   map[string]Combo
	handlers map[string]func() error
	nextID   int
	warn     func(format string, args ...any)
	started  bool
}

// NewHotkeyManager builds a manager. warn (optional) receives conflict and
// registration-failure messages so the app can surface them in the log/panel.
func NewHotkeyManager(warn func(string, ...any)) *HotkeyManager {
	if warn == nil {
		warn = func(string, ...any) {}
	}
	return &HotkeyManager{
		backend:  newHotkeyBackend(),
		byModule: map[string]int{},
		bySlot:   map[int]string{},
		combos:   map[string]Combo{},
		handlers: map[string]func() error{},
		warn:     warn,
	}
}

// Start begins dispatching hotkey presses. Safe to call more than once.
func (h *HotkeyManager) Start() {
	h.mu.Lock()
	if h.started {
		h.mu.Unlock()
		return
	}
	h.started = true
	ch := make(chan int, 16)
	h.mu.Unlock()

	go h.backend.run(ch)
	go func() {
		for id := range ch {
			h.mu.Lock()
			mod := h.bySlot[id]
			fn := h.handlers[mod]
			h.mu.Unlock()
			if fn != nil {
				go func(f func() error) { _ = f() }(fn)
			}
		}
	}()
}

// Bind registers (or re-registers) a module hotkey.
//
// Passing "" unregisters. A conflict with another application, or with a
// hotkey already owned by another module, is reported through warn and the
// binding is skipped — it never aborts startup.
//
// The backend calls happen outside h.mu. A threaded backend (Windows) blocks
// until the pump thread answers, so doing that under the lock would stall
// every other caller. The slot is reserved under the lock first and rolled
// back if registration fails, which keeps conflict detection intact.
func (h *HotkeyManager) Bind(module, hotkey string, fn func() error) {
	// Phase 1: decide under the lock.
	h.mu.Lock()
	if fn != nil {
		h.handlers[module] = fn
	}
	old, hasOld := h.byModule[module]
	if hasOld {
		delete(h.bySlot, old)
		delete(h.byModule, module)
		delete(h.combos, module)
	}

	combo, ok, err := ParseHotkey(hotkey)
	if err != nil {
		h.mu.Unlock()
		h.warn("模块 %s 的热键 %q 无效: %v", module, hotkey, err)
		h.dropOld(old, hasOld)
		return
	}
	if !ok { // empty = disabled
		h.mu.Unlock()
		h.dropOld(old, hasOld)
		return
	}

	// Reject duplicates inside our own process.
	for other, existing := range h.combos {
		if existing.Mods == combo.Mods && existing.VK == combo.VK {
			h.mu.Unlock()
			h.warn("热键 %s 冲突: 模块 %s 已占用，模块 %s 未绑定", combo.Text, other, module)
			h.dropOld(old, hasOld)
			return
		}
	}

	// Reserve the slot so a concurrent Bind cannot claim the same combo.
	id := h.nextID
	h.nextID++
	h.byModule[module] = id
	h.bySlot[id] = module
	h.combos[module] = combo
	h.mu.Unlock()

	// Phase 2: talk to the backend without holding the lock.
	h.dropOld(old, hasOld)
	if err := h.backend.register(id, combo); err != nil {
		h.mu.Lock()
		// Roll back only if the slot is still ours (a newer Bind may have
		// already replaced it).
		if cur, ok := h.byModule[module]; ok && cur == id {
			delete(h.byModule, module)
			delete(h.bySlot, id)
			delete(h.combos, module)
		}
		h.mu.Unlock()
		h.warn("热键 %s 注册失败（可能被其它程序占用）: %v", combo.Text, err)
	}
}

// dropOld removes a stale registration from the backend.
func (h *HotkeyManager) dropOld(id int, has bool) {
	if has {
		_ = h.backend.unregister(id)
	}
}

// Unbind removes a module's hotkey registration.
func (h *HotkeyManager) Unbind(module string) {
	h.mu.Lock()
	id, ok := h.byModule[module]
	delete(h.bySlot, id)
	delete(h.byModule, module)
	delete(h.combos, module)
	delete(h.handlers, module)
	h.mu.Unlock()

	if ok {
		_ = h.backend.unregister(id)
	}
}

// Combo returns the active combo for a module, if any.
func (h *HotkeyManager) Combo(module string) (Combo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.combos[module]
	return c, ok
}

// Conflicts reports hotkeys claimed by more than one module.
func (h *HotkeyManager) Conflicts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[Combo][]string{}
	for mod, c := range h.combos {
		seen[c] = append(seen[c], mod)
	}
	var out []string
	for c, mods := range seen {
		if len(mods) > 1 {
			out = append(out, fmt.Sprintf("%s: %s", c.Text, strings.Join(mods, ", ")))
		}
	}
	return out
}

// Rebind refreshes every module binding from the supplied hotkey strings.
func (h *HotkeyManager) Rebind(get func(module string) (string, func() error)) {
	h.mu.Lock()
	mods := make([]string, 0, len(h.handlers))
	for m := range h.handlers {
		mods = append(mods, m)
	}
	h.mu.Unlock()

	for _, m := range mods {
		hk, fn := get(m)
		h.Bind(m, hk, fn)
	}
}

// Stop releases every registration and shuts the backend down.
//
// The backend calls run outside h.mu: with a threaded backend (Windows) each
// call now waits for the pump thread, so holding the lock across them would
// stall every Bind/Combo/Conflicts caller during shutdown.
//
// Order matters: registrations are unregistered while the pump is still alive,
// then close() joins the thread, so Stop returns only after every hotkey is
// gone and no thread or goroutine is left behind.
func (h *HotkeyManager) Stop() {
	h.mu.Lock()
	started := h.started
	h.started = false
	ids := make([]int, 0, len(h.bySlot))
	for id := range h.bySlot {
		ids = append(ids, id)
	}
	h.bySlot = map[int]string{}
	h.byModule = map[string]int{}
	h.combos = map[string]Combo{}
	h.mu.Unlock()

	for _, id := range ids {
		_ = h.backend.unregister(id)
	}

	if started {
		h.backend.close()
		return
	}
	h.backend.close()
}
