package clipboard

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// panelEntriesLimit is how many history rows State() exposes to the panel:
// enough to browse recent history, small enough that every state broadcast
// stays a few KB regardless of how large the copied payloads were (previews
// are capped by maxPreview, payloads never leave the cache files).
const panelEntriesLimit = 30

// PanelEntry is one row of the history list surfaced through
// State()["entries"]. It is a bounded, metadata-only view: text entries carry
// a flattened excerpt, image/file entries a description built from the size or
// file name already stored on the Entry — never the payload itself.
type PanelEntry struct {
	ID      int    `json:"id"`
	Kind    string `json:"kind"`
	Preview string `json:"preview"`
	Time    string `json:"time"` // RFC3339；零值时间为空串
}

// panelEntryViews renders the newest panelEntriesLimit entries, newest first,
// matching the order the panel's history list displays.
func panelEntryViews(all []Entry) []PanelEntry {
	if n := len(all); n > panelEntriesLimit {
		all = all[n-panelEntriesLimit:]
	}
	out := make([]PanelEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		when := ""
		if !e.Timestamp.IsZero() {
			when = e.Timestamp.Format(time.RFC3339)
		}
		out = append(out, PanelEntry{
			ID:      e.ID,
			Kind:    string(e.Kind),
			Preview: entrySummary(e),
			Time:    when,
		})
	}
	return out
}

// entryIDFromParams parses the "id" parameter of the per-entry actions.
// 缺失/非法的 ID 必须给出中文错误——面板把错误原文 toast 给用户。
func entryIDFromParams(params map[string]string) (int, error) {
	raw := ""
	if len(params) > 0 {
		raw = params[paramID]
	}
	if raw == "" {
		return 0, errors.New("clipboard: 缺少条目 ID 参数")
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("clipboard: 条目 ID %q 无效", raw)
	}
	return id, nil
}

// deleteEntryFromParams implements the delete_entry action: it removes one
// entry by id. The onDelete callback (registered in Init) unlinks the entry's
// image cache file, so the disk budget shrinks with the list.
func (f *Feature) deleteEntryFromParams(params map[string]string) error {
	id, err := entryIDFromParams(params)
	if err != nil {
		return err
	}
	if _, ok := f.hist.Get(id); !ok {
		return fmt.Errorf("clipboard: 条目 %d 不存在（可能已被删除或清理）", id)
	}
	f.hist.Delete(id)
	f.ctx.Logger.Info("已删除历史条目", "module", moduleID, "id", id)
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// writeEntryFromParams implements the write_entry action: it writes one entry
// back onto the system clipboard through the same path as the native viewer
// and the "写回最近一条" action.
//
// File entries rely on winui.ClipboardFileDrop (CF_HDROP), a Windows-only
// channel: 非 Windows 平台 winui 返回“仅 Windows 支持…”的中文错误，这里包上
// 条目上下文后原样返回，面板据此提示用户。
func (f *Feature) writeEntryFromParams(params map[string]string) error {
	id, err := entryIDFromParams(params)
	if err != nil {
		return err
	}
	e, ok := f.hist.Get(id)
	if !ok {
		return fmt.Errorf("clipboard: 条目 %d 不存在（可能已被删除或清理）", id)
	}
	if err := f.writeBack(e); err != nil {
		return fmt.Errorf("clipboard: 写回条目 %d 失败: %w", id, err)
	}
	return nil
}
