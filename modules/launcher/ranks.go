package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// rankEntry 是一条候选命令的累积使用量、置顶标记与用户别名。
// Aliases 允许用户给候选添加别名/拼音首字母缩写（如给"记事本"加 "jsb"），
// 搜索时与 Label 同等参与匹配。旧版 JSON 无此字段，反序列化后为零值，兼容。
type rankEntry struct {
	Key     string   `json:"key"`
	Count   int      `json:"count"`
	Pinned  bool     `json:"pinned,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// rankStore 持久化候选命令的打开次数与"固定到前方"标记，用于搜索排序：
// 置顶项永远最前，其次打开次数降序，最后才轮到文本匹配度。数据落在模块
// DataDir 下的一个 JSON 文件里，进程重启后依然生效。
type rankStore struct {
	mu      sync.Mutex
	entries map[string]*rankEntry
	path    string
}

func newRankStore(dir string) *rankStore {
	// 空目录 = 不落盘（测试与降级场景）：path 置空而非 filepath.Join("", ...)
	// （后者会得到相对路径 "launcher_ranks.json"，导致误写工作目录）。
	path := ""
	if dir != "" {
		path = filepath.Join(dir, "launcher_ranks.json")
	}
	return &rankStore{
		entries: map[string]*rankEntry{},
		path:    path,
	}
}

// load 读取既有排行榜；文件缺失或损坏时静默降级为空表（排行榜只是排序优化，
// 不构成启动失败的先决条件）。
func (s *rankStore) load() {
	if s == nil || s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var list []rankEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range list {
		s.entries[list[i].Key] = &list[i]
	}
}

// get 返回某命令的打开次数、置顶标记与别名列表。
func (s *rankStore) get(key string) (count int, pinned bool, aliases []string) {
	if s == nil {
		return 0, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		return e.Count, e.Pinned, e.Aliases
	}
	return 0, false, nil
}

// getAliases 返回某命令的别名列表（搜索匹配用）。
func (s *rankStore) getAliases(key string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		return e.Aliases
	}
	return nil
}

// setAliases 覆盖某命令的别名列表并落盘。空列表清除别名。
func (s *rankStore) setAliases(key string, aliases []string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		if len(aliases) == 0 {
			return // 无既有条目且无别名，无需创建
		}
		e = &rankEntry{Key: key}
		s.entries[key] = e
	}
	e.Aliases = aliases
	s.saveLocked()
}

// bump 累加一次打开计数并落盘。
func (s *rankStore) bump(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		e = &rankEntry{Key: key}
		s.entries[key] = e
	}
	e.Count++
	s.saveLocked()
}

// clear 清空排行榜（打开次数、置顶与别名）并落盘。
func (s *rankStore) clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = map[string]*rankEntry{}
	s.saveLocked()
}

// setPin 设置/取消"固定到前方"并落盘。
func (s *rankStore) setPin(key string, on bool) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		e = &rankEntry{Key: key}
		s.entries[key] = e
	}
	e.Pinned = on
	s.saveLocked()
}

// saveLocked 在持锁前提下把整表序列化落盘。排序只为了让文件可读，不影响
// 内存中的查找语义。
func (s *rankStore) saveLocked() {
	if s.path == "" {
		return
	}
	list := make([]rankEntry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, *e)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned
		}
		return list[i].Count > list[j].Count
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}
