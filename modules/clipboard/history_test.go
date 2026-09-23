package clipboard

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// textsOf 提取条目文本序列，便于按顺序断言历史内容。
func textsOf(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Text)
	}
	return out
}

// idsOf 提取条目 ID 序列，便于断言裁剪后剩下的到底是哪些条目。
func idsOf(entries []Entry) []int {
	out := make([]int, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// equalStrings 比较两个字符串切片是否完全相等。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// addWithTime 先入栈再回写时间戳，用于构造可控时间的条目（PruneOlder 依赖时间）。
func addWithTime(t *testing.T, h *History, text string, ts time.Time) Entry {
	t.Helper()
	e := h.AddText(text)
	if e.ID == 0 {
		t.Fatalf("添加文本 %q 失败：返回了零值 Entry，期望分配到自增 ID", text)
	}
	e.Timestamp = ts
	h.Update(e)
	return e
}

// waitChange 等待 onChange 回调累计触发 n 次，超时则报告实际次数。
func waitChange(t *testing.T, n *atomic.Int64, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < int64(want) {
		if time.Now().After(deadline) {
			t.Fatalf("等待变更回调超时：期望触发 %d 次，实际只触发 %d 次", want, n.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestHistoryAddKeepsOrderKindAndSize 守护：文本/图片入栈后 All() 的数量、顺序（最新在最后）、类型与大小都正确，空载荷不入栈。
func TestHistoryAddKeepsOrderKindAndSize(t *testing.T) {
	cases := []struct {
		name     string
		max      int
		setup    func(h *History)
		wantLen  int
		wantText []string
		wantKind []EntryKind
		wantSize []int
	}{
		{
			name:     "纯文本按入栈顺序排列，最新在最后",
			max:      10,
			setup:    func(h *History) { h.AddText("一"); h.AddText("二"); h.AddText("三") },
			wantLen:  3,
			wantText: []string{"一", "二", "三"},
			wantKind: []EntryKind{KindText, KindText, KindText},
			wantSize: []int{len("一"), len("二"), len("三")},
		},
		{
			name: "图片条目记录 PNG 字节长度且排在文本之后",
			max:  10,
			setup: func(h *History) {
				h.AddText("文本")
				h.AddImage([]byte{0x89, 0x50, 0x4E, 0x47})
				h.AddImage([]byte{0x89, 0x50})
			},
			wantLen:  3,
			wantText: []string{"文本", "", ""},
			wantKind: []EntryKind{KindText, KindImage, KindImage},
			wantSize: []int{len("文本"), 4, 2},
		},
		{
			name:     "空文本不入栈",
			max:      10,
			setup:    func(h *History) { h.AddText(""); h.AddText("有效") },
			wantLen:  1,
			wantText: []string{"有效"},
			wantKind: []EntryKind{KindText},
			wantSize: []int{len("有效")},
		},
		{
			name: "空图片不入栈",
			max:  10,
			setup: func(h *History) {
				h.AddImage(nil)
				h.AddImage([]byte{})
				h.AddImage([]byte{0x01})
			},
			wantLen:  1,
			wantText: []string{""},
			wantKind: []EntryKind{KindImage},
			wantSize: []int{1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHistory(tc.max)
			tc.setup(h)

			got := h.All()
			if len(got) != tc.wantLen {
				t.Fatalf("条目数量不符：期望 %d 条，实际 %d 条，内容=%v", tc.wantLen, len(got), textsOf(got))
			}
			if !equalStrings(textsOf(got), tc.wantText) {
				t.Errorf("条目顺序/内容不符：期望 %v，实际 %v", tc.wantText, textsOf(got))
			}
			for i, e := range got {
				if e.Kind != tc.wantKind[i] {
					t.Errorf("第 %d 条类型不符：期望 %q，实际 %q", i, tc.wantKind[i], e.Kind)
				}
				if e.Size != tc.wantSize[i] {
					t.Errorf("第 %d 条大小不符：期望 %d，实际 %d", i, tc.wantSize[i], e.Size)
				}
				if e.ID <= 0 {
					t.Errorf("第 %d 条 ID 非法：期望正数，实际 %d", i, e.ID)
				}
				if e.Timestamp.IsZero() {
					t.Errorf("第 %d 条时间戳为空：期望入栈时自动填充", i)
				}
			}
		})
	}
}

// TestHistoryAddTrimsToMax 守护：超过 max 上限时自动裁剪，只保留最新的 max 条，且 ID 仍然递增。
func TestHistoryAddTrimsToMax(t *testing.T) {
	cases := []struct {
		name     string
		max      int
		add      int
		wantLen  int
		wantText []string
	}{
		{name: "上限 3 入栈 5 条，保留最新 3 条", max: 3, add: 5, wantLen: 3, wantText: []string{"c", "d", "e"}},
		{name: "上限 1 只保留最后一条", max: 1, add: 4, wantLen: 1, wantText: []string{"d"}},
		{name: "未超上限时全部保留", max: 10, add: 3, wantLen: 3, wantText: []string{"a", "b", "c"}},
		{name: "非法上限回退到默认值后全部保留", max: 0, add: 3, wantLen: 3, wantText: []string{"a", "b", "c"}},
	}

	names := []string{"a", "b", "c", "d", "e"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHistory(tc.max)
			for i := 0; i < tc.add; i++ {
				h.AddText(names[i])
			}

			got := h.All()
			if len(got) != tc.wantLen {
				t.Fatalf("裁剪后数量不符：max=%d 入栈 %d 条，期望保留 %d 条，实际 %d 条，内容=%v",
					tc.max, tc.add, tc.wantLen, len(got), textsOf(got))
			}
			if !equalStrings(textsOf(got), tc.wantText) {
				t.Errorf("裁剪后内容不符：期望 %v（最新 %d 条），实际 %v", tc.wantText, tc.wantLen, textsOf(got))
			}
			ids := idsOf(got)
			for i := 1; i < len(ids); i++ {
				if ids[i] <= ids[i-1] {
					t.Errorf("裁剪后 ID 未保持递增：%v", ids)
					break
				}
			}
		})
	}
}

// TestHistoryResize 守护：Resize 扩大后不再裁剪、缩小后立即裁剪到新上限，且置顶条目永不被裁掉。
func TestHistoryResize(t *testing.T) {
	cases := []struct {
		name      string
		initMax   int
		seed      []string
		pinnedIdx []int // 需要置顶的条目下标
		resizeTo  int
		after     []string // Resize 之后追加的文本
		wantText  []string
	}{
		{
			name:    "扩大上限后新条目不再被裁剪",
			initMax: 2, seed: []string{"a", "b"}, resizeTo: 5, after: []string{"c", "d"},
			wantText: []string{"a", "b", "c", "d"},
		},
		{
			name:    "缩小上限立即裁剪，保留最新条目",
			initMax: 5, seed: []string{"a", "b", "c", "d"}, resizeTo: 2,
			wantText: []string{"c", "d"},
		},
		{
			name:    "缩小上限时置顶条目不被裁掉",
			initMax: 5, seed: []string{"a", "b", "c", "d"}, pinnedIdx: []int{0}, resizeTo: 2,
			wantText: []string{"a", "d"},
		},
		{
			name:    "缩小后再入栈仍遵守新上限",
			initMax: 5, seed: []string{"a", "b", "c", "d"}, resizeTo: 2, after: []string{"e"},
			wantText: []string{"d", "e"},
		},
		{
			name:    "非法上限回退默认值，条目全部保留",
			initMax: 5, seed: []string{"a", "b"}, resizeTo: 0,
			wantText: []string{"a", "b"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHistory(tc.initMax)
			added := make([]Entry, 0, len(tc.seed))
			for _, s := range tc.seed {
				added = append(added, h.AddText(s))
			}
			for _, idx := range tc.pinnedIdx {
				if idx >= len(added) {
					t.Fatalf("用例数据错误：置顶下标 %d 超出已入栈条目数 %d", idx, len(added))
				}
				e := added[idx]
				e.Pinned = true
				h.Update(e)
			}

			h.Resize(tc.resizeTo)
			for _, s := range tc.after {
				h.AddText(s)
			}

			got := textsOf(h.All())
			if !equalStrings(got, tc.wantText) {
				t.Errorf("Resize(%d) 后内容不符：期望 %v，实际 %v", tc.resizeTo, tc.wantText, got)
			}
			if tc.resizeTo > 0 && !containsPinned(h, tc.pinnedIdx, added) {
				t.Errorf("Resize(%d) 后置顶条目丢失：实际 %v", tc.resizeTo, got)
			}
		})
	}
}

// containsPinned 检查被置顶的条目是否仍在历史中。
func containsPinned(h *History, pinnedIdx []int, added []Entry) bool {
	if len(pinnedIdx) == 0 {
		return true
	}
	live := make(map[int]bool, len(pinnedIdx))
	for _, e := range h.All() {
		live[e.ID] = true
	}
	for _, idx := range pinnedIdx {
		if !live[added[idx].ID] {
			return false
		}
	}
	return true
}

// TestHistoryAddDeduplicates 守护：连续重复内容只刷新时间戳不新增条目，非相邻重复仍会入栈。
func TestHistoryAddDeduplicates(t *testing.T) {
	cases := []struct {
		name     string
		seed     []string
		wantLen  int
		wantText []string
	}{
		{name: "连续相同文本去重", seed: []string{"a", "a", "a"}, wantLen: 1, wantText: []string{"a"}},
		{name: "非相邻重复不去重", seed: []string{"a", "b", "a"}, wantLen: 3, wantText: []string{"a", "b", "a"}},
		{name: "不同内容正常入栈", seed: []string{"a", "b", "c"}, wantLen: 3, wantText: []string{"a", "b", "c"}},
		{name: "去重中间夹入新内容后再重复", seed: []string{"a", "a", "b", "b"}, wantLen: 2, wantText: []string{"a", "b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHistory(100)
			for _, s := range tc.seed {
				h.AddText(s)
			}

			got := h.All()
			if len(got) != tc.wantLen {
				t.Fatalf("去重后数量不符：入栈 %v，期望 %d 条，实际 %d 条，内容=%v",
					tc.seed, tc.wantLen, len(got), textsOf(got))
			}
			if !equalStrings(textsOf(got), tc.wantText) {
				t.Errorf("去重后内容不符：期望 %v，实际 %v", tc.wantText, textsOf(got))
			}
		})
	}

	t.Run("图片按字节内容去重", func(t *testing.T) {
		h := NewHistory(100)
		png := []byte{0x89, 0x50, 0x4E, 0x47}
		h.AddImage(png)
		again := h.AddImage(append([]byte{}, png...))
		if len(h.All()) != 1 {
			t.Fatalf("相同 PNG 应去重：期望 1 条，实际 %d 条", len(h.All()))
		}
		first := h.All()[0]
		if again.ID != first.ID {
			t.Errorf("重复图片应复用原条目 ID：期望 %d，实际 %d", first.ID, again.ID)
		}
		if !again.Timestamp.Before(first.Timestamp) && !again.Timestamp.Equal(first.Timestamp) {
			t.Errorf("重复图片返回的时间戳异常：期望不早于原条目 %v，实际 %v", first.Timestamp, again.Timestamp)
		}
		h.AddImage([]byte{0x89, 0x51})
		if len(h.All()) != 2 {
			t.Errorf("不同 PNG 应正常入栈：期望 2 条，实际 %d 条", len(h.All()))
		}
	})

	t.Run("重复内容刷新时间戳而非新增", func(t *testing.T) {
		h := NewHistory(100)
		first := addWithTime(t, h, "同文", time.Now().Add(-time.Hour))
		time.Sleep(2 * time.Millisecond)
		h.AddText("同文")

		got := h.All()
		if len(got) != 1 {
			t.Fatalf("重复内容不应新增条目：期望 1 条，实际 %d 条", len(got))
		}
		if got[0].ID != first.ID {
			t.Errorf("去重后 ID 应保持为原条目：期望 %d，实际 %d", first.ID, got[0].ID)
		}
		if !got[0].Timestamp.After(first.Timestamp) {
			t.Errorf("去重应刷新时间戳：原时间戳 %v，实际 %v", first.Timestamp, got[0].Timestamp)
		}
	})
}

// TestHistoryDeleteDeleteExceptClear 守护：Delete 精确删除单条、DeleteExcept 只保留指定 ID、Clear 清空但保留置顶条目。
func TestHistoryDeleteDeleteExceptClear(t *testing.T) {
	seed := func(t *testing.T) *History {
		t.Helper()
		h := NewHistory(100)
		h.AddText("a")
		h.AddText("b")
		h.AddText("c")
		return h
	}

	t.Run("Delete 删除指定条目", func(t *testing.T) {
		cases := []struct {
			name     string
			target   string
			wantText []string
		}{
			{name: "删除首条", target: "a", wantText: []string{"b", "c"}},
			{name: "删除中间条", target: "b", wantText: []string{"a", "c"}},
			{name: "删除末条", target: "c", wantText: []string{"a", "b"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := seed(t)
				var id int
				for _, e := range h.All() {
					if e.Text == tc.target {
						id = e.ID
					}
				}
				h.Delete(id)
				if got := textsOf(h.All()); !equalStrings(got, tc.wantText) {
					t.Errorf("删除 %q 后内容不符：期望 %v，实际 %v", tc.target, tc.wantText, got)
				}
			})
		}
	})

	t.Run("Delete 未知 ID 不影响历史", func(t *testing.T) {
		h := seed(t)
		h.Delete(99999)
		if got := textsOf(h.All()); !equalStrings(got, []string{"a", "b", "c"}) {
			t.Errorf("删除不存在的 ID 不应改动历史：期望 [a b c]，实际 %v", got)
		}
	})

	t.Run("DeleteExcept 只保留指定 ID", func(t *testing.T) {
		cases := []struct {
			name     string
			keepText []string // 期望被保留的文本（转成 ID 后传给 DeleteExcept）
			wantText []string
		}{
			{name: "保留一条", keepText: []string{"b"}, wantText: []string{"b"}},
			{name: "保留首末两条", keepText: []string{"a", "c"}, wantText: []string{"a", "c"}},
			{name: "保留全部", keepText: []string{"a", "b", "c"}, wantText: []string{"a", "b", "c"}},
			{name: "保留集合含不存在的 ID 不影响结果", keepText: []string{"b", "不存在的文本"}, wantText: []string{"b"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := seed(t)
				idOf := make(map[string]int, len(h.All()))
				for _, e := range h.All() {
					idOf[e.Text] = e.ID
				}
				keep := make(map[int]bool, len(tc.keepText))
				found := 0
				for _, txt := range tc.keepText {
					if id, ok := idOf[txt]; ok {
						keep[id] = true
						found++
					}
				}
				if found == 0 {
					t.Fatalf("用例数据错误：保留集合 %v 中没有任何一条存在于历史中", tc.keepText)
				}

				h.DeleteExcept(keep)
				got := textsOf(h.All())
				if !equalStrings(got, tc.wantText) {
					t.Errorf("DeleteExcept(%v) 后内容不符：期望 %v，实际 %v", tc.keepText, tc.wantText, got)
				}
			})
		}
	})

	t.Run("DeleteExcept 空集合清空全部", func(t *testing.T) {
		h := seed(t)
		h.DeleteExcept(nil)
		if got := h.All(); len(got) != 0 {
			t.Errorf("keep 为空时应清空全部：期望 0 条，实际 %d 条，内容=%v", len(got), textsOf(got))
		}
	})

	t.Run("DeleteExcept 忽略置顶标记", func(t *testing.T) {
		h := seed(t)
		var pinned int
		for _, e := range h.All() {
			if e.Text == "a" {
				pinned = e.ID
			}
		}
		pe, _ := h.Get(pinned)
		pe.Pinned = true
		h.Update(pe)
		h.DeleteExcept(map[int]bool{})
		if got := h.All(); len(got) != 0 {
			t.Errorf("DeleteExcept 应删除未列入保留集合的置顶条目：期望 0 条，实际 %d 条，内容=%v", len(got), textsOf(got))
		}
	})

	t.Run("Clear 清空但保留置顶条目", func(t *testing.T) {
		h := seed(t)
		var keepID int
		for _, e := range h.All() {
			if e.Text == "b" {
				keepID = e.ID
			}
		}
		ke, _ := h.Get(keepID)
		ke.Pinned = true
		h.Update(ke)

		h.Clear()
		got := h.All()
		if len(got) != 1 {
			t.Fatalf("Clear 后应只保留 1 条置顶条目：期望 1 条，实际 %d 条，内容=%v", len(got), textsOf(got))
		}
		if got[0].Text != "b" || !got[0].Pinned {
			t.Errorf("Clear 保留的条目不符：期望置顶的 \"b\"，实际 %q（Pinned=%v）", got[0].Text, got[0].Pinned)
		}
	})

	t.Run("Clear 无置顶条目时清空全部", func(t *testing.T) {
		h := seed(t)
		h.Clear()
		if got := h.All(); len(got) != 0 {
			t.Errorf("无置顶条目时 Clear 应清空：期望 0 条，实际 %d 条，内容=%v", len(got), textsOf(got))
		}
	})
}

// TestHistoryPruneOlder 守护：PruneOlder 只删除早于 cutoff 的非置顶条目，等于 cutoff 的也删除，置顶条目永不过期。
func TestHistoryPruneOlder(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		ages      []time.Duration // 相对 base 的偏移，负值为更早
		pinnedIdx []int
		cutoff    time.Time
		wantText  []string
	}{
		{
			name: "早于 cutoff 的被删除",
			ages: []time.Duration{-3 * time.Hour, -2 * time.Hour, -time.Hour, time.Hour},
			// a,b,c 早于 base，d 晚于 base
			cutoff:   base,
			wantText: []string{"d"},
		},
		{
			name:     "全部晚于 cutoff 时无删除",
			ages:     []time.Duration{time.Hour, 2 * time.Hour},
			cutoff:   base,
			wantText: []string{"a", "b"},
		},
		{
			name:     "全部早于 cutoff 时清空",
			ages:     []time.Duration{-time.Hour, -2 * time.Hour},
			cutoff:   base,
			wantText: []string{},
		},
		{
			name:     "等于 cutoff 的条目被删除（仅严格更晚才保留）",
			ages:     []time.Duration{0, time.Second},
			cutoff:   base,
			wantText: []string{"b"},
		},
		{
			name:      "置顶条目即使过期也保留",
			ages:      []time.Duration{-3 * time.Hour, -2 * time.Hour},
			pinnedIdx: []int{0},
			cutoff:    base,
			wantText:  []string{"a"},
		},
	}

	names := []string{"a", "b", "c", "d"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHistory(100)
			added := make([]Entry, 0, len(tc.ages))
			for i, age := range tc.ages {
				added = append(added, addWithTime(t, h, names[i], base.Add(age)))
			}
			for _, idx := range tc.pinnedIdx {
				e := added[idx]
				e.Pinned = true
				h.Update(e)
			}

			h.PruneOlder(tc.cutoff)

			got := textsOf(h.All())
			if !equalStrings(got, tc.wantText) {
				t.Errorf("PruneOlder(%v) 后内容不符：期望 %v，实际 %v", tc.cutoff, tc.wantText, got)
			}
			for _, e := range h.All() {
				if e.Pinned {
					continue
				}
				if !e.Timestamp.After(tc.cutoff) {
					t.Errorf("残留了已过期条目：%q 时间戳 %v 不晚于 cutoff %v", e.Text, e.Timestamp, tc.cutoff)
				}
			}
		})
	}

	t.Run("零值 cutoff 视为不裁剪", func(t *testing.T) {
		h := NewHistory(100)
		addWithTime(t, h, "旧", base.Add(-100*time.Hour))
		h.PruneOlder(time.Time{})
		if got := h.All(); len(got) != 1 {
			t.Errorf("零值 cutoff 不应裁剪：期望 1 条，实际 %d 条", len(got))
		}
	})
}

// TestHistoryGetAndUpdate 守护：Get 命中返回副本数据且未命中返回 false，Update 按 ID 原地替换字段。
func TestHistoryGetAndUpdate(t *testing.T) {
	t.Run("Get 命中与未命中", func(t *testing.T) {
		h := NewHistory(100)
		first := h.AddText("甲")
		h.AddText("乙")

		cases := []struct {
			name    string
			id      int
			wantOK  bool
			wantTxt string
		}{
			{name: "命中首条", id: first.ID, wantOK: true, wantTxt: "甲"},
			{name: "ID 为 0 未命中", id: 0, wantOK: false, wantTxt: ""},
			{name: "不存在的 ID 未命中", id: 424242, wantOK: false, wantTxt: ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := h.Get(tc.id)
				if ok != tc.wantOK {
					t.Fatalf("Get(%d) 命中状态不符：期望 %v，实际 %v", tc.id, tc.wantOK, ok)
				}
				if !tc.wantOK {
					if got.ID != 0 || got.Text != "" {
						t.Errorf("未命中时应返回零值 Entry：实际 ID=%d Text=%q", got.ID, got.Text)
					}
					return
				}
				if got.Text != tc.wantTxt {
					t.Errorf("Get(%d) 文本不符：期望 %q，实际 %q", tc.id, tc.wantTxt, got.Text)
				}
			})
		}
	})

	t.Run("Get 返回副本，改动不影响历史", func(t *testing.T) {
		h := NewHistory(100)
		a := h.AddText("原值")
		got, ok := h.Get(a.ID)
		if !ok {
			t.Fatalf("Get(%d) 应命中，实际未命中", a.ID)
		}
		got.Text = "被改坏"
		if cur, _ := h.Get(a.ID); cur.Text != "原值" {
			t.Errorf("修改 Get 返回值不应影响历史：期望 \"原值\"，实际 %q", cur.Text)
		}
	})

	t.Run("Update 按 ID 原地替换", func(t *testing.T) {
		cases := []struct {
			name     string
			mutate   func(Entry) Entry
			wantTxt  string
			wantSize int
			wantPin  bool
		}{
			{
				name:     "修改文本并重算大小",
				mutate:   func(e Entry) Entry { e.Text = "新文本"; return e },
				wantTxt:  "新文本",
				wantSize: len("新文本"),
			},
			{
				name:     "置顶条目",
				mutate:   func(e Entry) Entry { e.Pinned = true; return e },
				wantTxt:  "旧文本",
				wantSize: len("旧文本"),
				wantPin:  true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := NewHistory(100)
				first := h.AddText("旧文本")
				h.AddText("无关")
				h.Update(tc.mutate(first))

				got, ok := h.Get(first.ID)
				if !ok {
					t.Fatalf("Update 后条目丢失：ID=%d", first.ID)
				}
				if got.Text != tc.wantTxt {
					t.Errorf("Update 后文本不符：期望 %q，实际 %q", tc.wantTxt, got.Text)
				}
				if got.Size != tc.wantSize {
					t.Errorf("Update 后大小不符：期望 %d，实际 %d", tc.wantSize, got.Size)
				}
				if got.Pinned != tc.wantPin {
					t.Errorf("Update 后置顶状态不符：期望 %v，实际 %v", tc.wantPin, got.Pinned)
				}
				if got.ID != first.ID {
					t.Errorf("Update 不应改变 ID：期望 %d，实际 %d", first.ID, got.ID)
				}
				if all := h.All(); len(all) != 2 {
					t.Errorf("Update 不应改变条目数量：期望 2 条，实际 %d 条", len(all))
				}
			})
		}
	})

	t.Run("Update 未知 ID 是空操作", func(t *testing.T) {
		h := NewHistory(100)
		h.AddText("保持")
		h.Update(Entry{ID: 99999, Kind: KindText, Text: "幽灵"})
		if got := textsOf(h.All()); !equalStrings(got, []string{"保持"}) {
			t.Errorf("Update 未知 ID 不应改变历史：期望 [保持]，实际 %v", got)
		}
	})
}

// TestHistorySetOnChange 守护：每次变更（增/删/清）都会触发一次 onChange 回调，且回调在 goroutine 中执行不阻塞写入。
func TestHistorySetOnChange(t *testing.T) {
	var fired atomic.Int64
	h := NewHistory(100)
	h.SetOnChange(func() { fired.Add(1) })

	h.AddText("一")
	h.AddText("二")
	waitChange(t, &fired, 2)

	first := h.All()[0]
	h.Delete(first.ID)
	waitChange(t, &fired, 3)

	h.Clear()
	waitChange(t, &fired, 4)

	time.Sleep(50 * time.Millisecond)
	if got := fired.Load(); got != 4 {
		t.Errorf("变更回调次数不符：4 次变更期望触发 4 次，实际 %d 次", got)
	}

	// 去重命中（未产生变更）不应触发回调。
	h.AddText("重复")
	waitChange(t, &fired, 5)
	h.AddText("重复")
	time.Sleep(50 * time.Millisecond)
	if got := fired.Load(); got != 5 {
		t.Errorf("重复内容去重时不应触发回调：期望 5 次，实际 %d 次", got)
	}
}

// TestHistoryConcurrentAddText 守护：多 goroutine 并发 AddText 在 -race 下无数据竞争，且最终条目数被正确限制在 max 以内。
func TestHistoryConcurrentAddText(t *testing.T) {
	const (
		goroutines = 32
		perG       = 20
		max        = 50
	)

	h := NewHistory(max)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				h.AddText(fmt.Sprintf("g%d-%d", g, i))
				h.All()
				h.Get(1)
			}
		}(g)
	}
	wg.Wait()

	got := h.All()
	if len(got) != max {
		t.Fatalf("并发写入后条目数不符：期望恰好 %d 条（max=%d），实际 %d 条", max, max, len(got))
	}
	ids := idsOf(got)
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("并发写入后 ID 未保持递增：%v", ids)
		}
	}
	if ids[0] != goroutines*perG-max+1 {
		t.Errorf("并发写入后应保留最新 %d 条：期望最小 ID 为 %d，实际 %d",
			max, goroutines*perG-max+1, ids[0])
	}
	for _, e := range got {
		if e.Text == "" || e.ID == 0 || e.Timestamp.IsZero() {
			t.Errorf("并发写入产生了不完整条目：ID=%d Text=%q Timestamp=%v", e.ID, e.Text, e.Timestamp)
		}
	}
}

// TestHistoryConcurrentMixedOps 守护：读（All/Get）写（AddText/Delete/Update/PruneOlder）混合并发时 -race 无竞态，且历史始终自洽。
func TestHistoryConcurrentMixedOps(t *testing.T) {
	h := NewHistory(40)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.AddText(fmt.Sprintf("写-%d", i%37))
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if all := h.All(); len(all) > 0 {
				h.Delete(all[0].ID)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for _, e := range h.All() {
				e.Pinned = i%2 == 0
				h.Update(e)
			}
			h.PruneOlder(time.Now().Add(-time.Hour))
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = h.All()
				_, _ = h.Get(i)
			}
		}()
	}
	wg.Wait()

	got := h.All()
	if len(got) > 40 {
		t.Errorf("并发混合操作后条目数超出上限：期望不超过 40 条，实际 %d 条", len(got))
	}
	seen := make(map[int]bool, len(got))
	for _, e := range got {
		if seen[e.ID] {
			t.Errorf("并发混合操作后出现了重复 ID：%d", e.ID)
		}
		seen[e.ID] = true
		if e.ID == 0 {
			t.Errorf("并发混合操作产生了零 ID 条目：Text=%q", e.Text)
		}
	}
	// 历史仍在正常工作：取消置顶后清空，应无残留。
	for _, e := range h.All() {
		e.Pinned = false
		h.Update(e)
	}
	h.Clear()
	if left := h.All(); len(left) != 0 {
		t.Errorf("Clear 后仍残留条目：期望 0 条，实际 %d 条，内容=%v", len(left), textsOf(left))
	}
}
