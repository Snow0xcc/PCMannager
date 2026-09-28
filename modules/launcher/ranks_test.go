package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRankStoreAliasesRoundTrip 守护别名的存取与落盘往返：set 后 get 一致，
// 文件里能读回同样的数据，空列表清除别名。
func TestRankStoreAliasesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newRankStore(dir)
	s.setAliases("k1", []string{"jsb", "notepad"})

	if got := s.getAliases("k1"); len(got) != 2 || got[0] != "jsb" || got[1] != "notepad" {
		t.Fatalf("内存读取别名 = %v, 期望 [jsb notepad]", got)
	}

	// 新 store 从文件加载后应读回同样数据。
	s2 := newRankStore(dir)
	s2.load()
	if got := s2.getAliases("k1"); len(got) != 2 || got[0] != "jsb" {
		t.Fatalf("落盘往返别名 = %v, 期望 [jsb notepad]", got)
	}

	// 空列表清除别名。
	s.setAliases("k1", nil)
	if got := s.getAliases("k1"); len(got) != 0 {
		t.Fatalf("清空后应无别名, 实际 %v", got)
	}
	s3 := newRankStore(dir)
	s3.load()
	if got := s3.getAliases("k1"); len(got) != 0 {
		t.Fatalf("落盘往返清空后应无别名, 实际 %v", got)
	}
}

// TestRankStoreLegacyJSONCompat 守护旧版 JSON 兼容：无 aliases 字段的历史文件
// 加载后不报错，count/pinned 正常读回。
func TestRankStoreLegacyJSONCompat(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`[{"key":"k1","count":7,"pinned":true}]`)
	if err := os.WriteFile(filepath.Join(dir, "launcher_ranks.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	s := newRankStore(dir)
	s.load()
	count, pinned, aliases := s.get("k1")
	if count != 7 || !pinned || len(aliases) != 0 {
		t.Fatalf("旧 JSON 读取 = (%d, %v, %v), 期望 (7, true, [])", count, pinned, aliases)
	}
}

// TestRankStoreSavedJSONHasAliasesField 守护落盘结构：aliases 字段名稳定，
// 拼写错误会静默丢数据，用真实 JSON 解析守护字段名。
func TestRankStoreSavedJSONHasAliasesField(t *testing.T) {
	dir := t.TempDir()
	s := newRankStore(dir)
	s.setAliases("k1", []string{"ab"})
	data, err := os.ReadFile(filepath.Join(dir, "launcher_ranks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Key     string   `json:"key"`
		Count   int      `json:"count"`
		Pinned  bool     `json:"pinned"`
		Aliases []string `json:"aliases"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("解析落盘 JSON 失败: %v", err)
	}
	if len(list) != 1 || list[0].Key != "k1" || len(list[0].Aliases) != 1 || list[0].Aliases[0] != "ab" {
		t.Fatalf("落盘结构不符: %s", data)
	}
}
