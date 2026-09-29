package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// superStore 持久化「超级面板」的固定项（command key 列表）。超级面板是
// 全局悬浮磁贴池：用户把高频命令固定进来，随时一键执行，与搜索面板的
// 置顶（pinned，只影响排序）是独立语义。
//
// 只存 key 不存 command：命令表随模块启停重建，key→command 的反查在展示时
// 用当前活表进行，消失的 key 静默跳过（而不是存住过期动作）。
type superStore struct {
	mu   sync.Mutex
	keys []string
	path string
}

func newSuperStore(dir string) *superStore {
	// 空目录 = 不落盘（测试与降级场景），与 rankStore 同一约定。
	path := ""
	if dir != "" {
		path = filepath.Join(dir, "launcher_super.json")
	}
	return &superStore{path: path}
}

// load 读取既有固定项；文件缺失或损坏时静默降级为空表。
func (s *superStore) load() {
	if s == nil || s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = keys
}

// list 返回固定项 key 的副本（顺序即展示顺序）。
func (s *superStore) list() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys...)
}

// has 报告某 key 是否已在超级面板。
func (s *superStore) has(key string) bool {
	if s == nil || key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keys {
		if k == key {
			return true
		}
	}
	return false
}

// add 追加一个 key（去重，已存在则不动）并落盘。
func (s *superStore) add(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keys {
		if k == key {
			return
		}
	}
	s.keys = append(s.keys, key)
	s.saveLocked()
}

// remove 移除一个 key 并落盘。
func (s *superStore) remove(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.keys[:0]
	for _, k := range s.keys {
		if k != key {
			out = append(out, k)
		}
	}
	s.keys = out
	s.saveLocked()
}

// resolveKeys 把固定 key 列表反查为活命令（展示时按 key 顺序还原；消失的
// key 静默跳过）。
//
// 放在平台无关文件：反查只依赖 f.commands 这张内存表，与 Win32 无关，跨平台
// 行为必须一致。早期误放在 super_windows.go 并在非 Windows 侧给 nil 桩，
// 会让依赖它的测试在 Linux runner 上失败（CI 正是因此红掉）。
func (f *Feature) resolveKeys(keys []string) []command {
	f.mu.Lock()
	cmds := f.commands
	f.mu.Unlock()
	byKey := make(map[string]command, len(cmds))
	for _, c := range cmds {
		byKey[c.key()] = c
	}
	out := make([]command, 0, len(keys))
	for _, k := range keys {
		if c, ok := byKey[k]; ok {
			out = append(out, c)
		}
	}
	return out
}

// saveLocked 在持锁前提下落盘。
func (s *superStore) saveLocked() {
	if s.path == "" {
		return
	}
	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}
