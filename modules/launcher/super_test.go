package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// TestSuperStoreRoundTrip 守护超级面板固定项的存取与落盘往返：add 去重、
// remove 移除、list 保序、跨 store 加载一致。
func TestSuperStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newSuperStore(dir)
	s.add("clipboard")
	s.add("screenshot/clear")
	s.add("clipboard") // 去重

	if got := s.list(); len(got) != 2 || got[0] != "clipboard" || got[1] != "screenshot/clear" {
		t.Fatalf("list = %v, 期望 [clipboard screenshot/clear]", got)
	}
	if !s.has("clipboard") || s.has("nonexistent") {
		t.Fatal("has 判定不符")
	}

	s2 := newSuperStore(dir)
	s2.load()
	if got := s2.list(); len(got) != 2 || got[0] != "clipboard" {
		t.Fatalf("落盘往返 = %v", got)
	}

	s2.remove("clipboard")
	if got := s2.list(); len(got) != 1 || got[0] != "screenshot/clear" {
		t.Fatalf("remove 后 = %v", got)
	}
	s3 := newSuperStore(dir)
	s3.load()
	if got := s3.list(); len(got) != 1 {
		t.Fatalf("remove 落盘往返 = %v", got)
	}
}

// TestSuperStoreNoPathIsMemoryOnly 守护空目录语义：不落盘仅内存，load 不报错。
func TestSuperStoreNoPathIsMemoryOnly(t *testing.T) {
	s := newSuperStore("")
	s.add("k1")
	if got := s.list(); len(got) != 1 {
		t.Fatalf("内存模式 list = %v", got)
	}
	s.load() // 无 path，安全
}

// TestResolveKeys 守护 key 反查：活表里的 key 还原为 command，消失的 key 跳过，
// 顺序与 keys 输入一致。
func TestResolveKeys(t *testing.T) {
	f := &Feature{}
	f.commands = []command{
		{Label: "A", Kind: "open", ModuleID: "modA"},
		{Label: "B", Kind: "url", URL: "https://b.example"},
		{Label: "C", Kind: "app", Path: "C:\\c.exe"},
	}
	keys := []string{
		(command{Kind: "url", URL: "https://b.example"}).key(),
		"gone", // 已消失的 key
		(command{Kind: "open", ModuleID: "modA"}).key(),
	}
	got := f.resolveKeys(keys)
	if len(got) != 2 {
		t.Fatalf("应只还原 2 条（跳过消失的）, 实际 %d", len(got))
	}
	if got[0].Label != "B" || got[1].Label != "A" {
		t.Fatalf("顺序应与 keys 输入一致: %q %q", got[0].Label, got[1].Label)
	}
}

// TestExtraHotkeysDeclared 守护超级面板热键声明：alt+p 必须注册（否则热键
// 框架不会绑定），Label 用于错误槽展示。
func TestExtraHotkeysDeclared(t *testing.T) {
	f := &Feature{}
	hks := f.ExtraHotkeys()
	if len(hks) != 1 {
		t.Fatalf("应声明 1 个扩展热键, 实际 %d", len(hks))
	}
	if hks[0].Hotkey != "alt+p" || hks[0].Label == "" || hks[0].Fire == nil {
		t.Fatalf("热键声明不完整: %+v", hks[0])
	}
}

// TestSuperStoreSavedShape 守护落盘结构是 key 字符串数组。
func TestSuperStoreSavedShape(t *testing.T) {
	dir := t.TempDir()
	s := newSuperStore(dir)
	s.add("k1")
	data, err := os.ReadFile(filepath.Join(dir, "launcher_super.json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("落盘应为字符串数组: %v", err)
	}
	if len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("落盘内容不符: %s", data)
	}
}

// 编译期守护：Feature 实现了 ExtraHotkeysProvider。
var _ core.ExtraHotkeysProvider = (*Feature)(nil)
