package screenshot

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// panelEntriesLimit is how many history rows State() exposes to the panel:
// enough to browse recent captures, small enough that every state broadcast
// stays small even with max_history at its ceiling — file metadata is only
// fetched for the rows that make the cut (C2-5, mirrors clipboard C2-1).
const panelEntriesLimit = 30

// recordingNamePrefix marks screen-recording files (GIF/MP4). It lives with
// the panel view so all three module-owned filename shapes are classified in
// one table; the save paths keep their literals.
const recordingNamePrefix = "recording_"

// shotKindPrefixes maps filename prefixes to the panel-facing category label:
// 普通截图 / 滚动长截图 / 录屏 are the three workflows this module writes.
var shotKindPrefixes = []struct {
	prefix string
	kind   string
}{
	{savedNamePrefix, "screenshot"},
	{longshotNamePrefix, "longshot"},
	{recordingNamePrefix, "recording"},
}

// PanelShot is one row of the saved-captures list surfaced through
// State()["entries"] (C2-5). It is a metadata-only view: the absolute path is
// deliberately left out — the panel needs the name, the category, the size and
// the timestamp, and exposing fewer fields keeps the information surface small.
type PanelShot struct {
	Name string `json:"name"` // 文件名（不含目录）
	Kind string `json:"kind"` // screenshot / longshot / recording / other
	Size int64  `json:"size"` // 字节；文件已不可读时为 0
	Time string `json:"time"` // RFC3339；取不到时为空串
}

// panelShotViews renders the newest panelEntriesLimit captures, newest first,
// matching the order the panel's history list displays. paths must be the
// snapshot of f.saved taken under f.mu; the file stats happen here, outside
// the lock, because I/O never belongs in a critical section. The result is
// never nil so the panel can rely on an empty array for "no history yet".
func panelShotViews(paths []string) []PanelShot {
	if n := len(paths); n > panelEntriesLimit {
		paths = paths[n-panelEntriesLimit:]
	}
	out := make([]PanelShot, 0, len(paths))
	for i := len(paths) - 1; i >= 0; i-- {
		name := filepath.Base(paths[i])
		shot := PanelShot{
			Name: name,
			Kind: shotKind(name),
		}
		info, statErr := os.Stat(paths[i])
		if statErr == nil {
			shot.Size = info.Size()
		}
		// 保存时刻优先取文件名内嵌的毫秒时间戳（写入时生成的，与重命名/
		// 复制无关）；解析不出再退回文件修改时间，仍取不到就留空。
		if ts, ok := shotTimestamp(name); ok {
			shot.Time = ts.Format(time.RFC3339)
		} else if statErr == nil {
			shot.Time = info.ModTime().Format(time.RFC3339)
		}
		out = append(out, shot)
	}
	return out
}

// shotKind classifies a saved file by its filename prefix; names outside the
// three known shapes (they cannot appear through the normal save paths, but
// defensively keep a label) fall back to "other".
func shotKind(name string) string {
	for _, p := range shotKindPrefixes {
		if strings.HasPrefix(name, p.prefix) {
			return p.kind
		}
	}
	return "other"
}

// shotTimestamp extracts the save time embedded in a module filename
// (prefix_<unixmilli><ext>). ok is false for names that do not follow the
// pattern; nothing this module writes misses it.
func shotTimestamp(name string) (time.Time, bool) {
	stem := ""
	for _, p := range shotKindPrefixes {
		if strings.HasPrefix(name, p.prefix) {
			stem = strings.TrimPrefix(name, p.prefix)
			break
		}
	}
	if stem == "" {
		return time.Time{}, false
	}
	ext := filepath.Ext(name)
	if ext == "" || !strings.HasSuffix(stem, ext) {
		return time.Time{}, false
	}
	stem = strings.TrimSuffix(stem, ext)
	ms, err := strconv.ParseInt(stem, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}
