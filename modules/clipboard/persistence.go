package clipboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	historyStoreName = "clipboard_history.json"
	maxHistoryStore  = 64 << 20

	// historySaveDelay debounces full-history rewrites. Copying several items in
	// a row (or a clipboard manager writing both text and an image) would
	// otherwise serialize and fsync the whole history on every single change.
	historySaveDelay = 300 * time.Millisecond
)

type historySnapshot struct {
	Version int     `json:"version"`
	NextID  int     `json:"next_id"`
	Entries []Entry `json:"entries"`
}

func (f *Feature) historyStorePath() string {
	if f.ctx == nil || f.ctx.DataDir == "" {
		return ""
	}
	return filepath.Join(f.ctx.DataDir, historyStoreName)
}

func (f *Feature) restoreHistory() error {
	path := f.historyStorePath()
	if path == "" {
		return nil
	}
	if !f.snapshot().PersistHistory {
		return f.removeHistoryStore()
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取历史文件失败: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxHistoryStore+1))
	if err != nil {
		return fmt.Errorf("读取历史文件失败: %w", err)
	}
	if len(data) > maxHistoryStore {
		return fmt.Errorf("历史文件超过 %d MiB 上限", maxHistoryStore>>20)
	}
	var saved historySnapshot
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("解析历史文件失败: %w", err)
	}
	if saved.Version != 1 {
		return fmt.Errorf("不支持的历史文件版本 %d", saved.Version)
	}
	entries := make([]Entry, 0, len(saved.Entries))
	missingImages := 0
	for _, e := range saved.Entries {
		if e.ID <= 0 {
			return errors.New("历史文件包含无效条目 ID")
		}
		if e.Kind == KindImage {
			cleanPath := filepath.Clean(e.Path)
			if filepath.Dir(cleanPath) != filepath.Clean(f.cacheDir()) || !strings.HasPrefix(filepath.Base(cleanPath), cachePrefix) || filepath.Ext(cleanPath) != cacheExt {
				return errors.New("历史文件包含无效图片缓存路径")
			}
			// 缓存文件可能因手动清理/崩溃丢失；保留该条目只会得到一个
			// 无法写回的坏行，直接跳过并在下次落盘时把它从索引里删掉。
			if _, statErr := os.Stat(cleanPath); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					missingImages++
					continue
				}
				return fmt.Errorf("检查图片缓存失败: %w", statErr)
			}
		} else if e.Kind != KindText && e.Kind != KindFile {
			return fmt.Errorf("历史文件包含未知条目类型 %q", e.Kind)
		}
		entries = append(entries, e)
	}
	if missingImages > 0 && f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Warn("历史文件中的图片缓存已缺失，跳过对应条目", "module", moduleID, "count", missingImages)
	}
	f.hist.restore(entries, saved.NextID)
	return nil
}

func (f *Feature) persistHistory() error {
	if f.ctx == nil {
		return nil
	}
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	if !f.snapshot().PersistHistory {
		return nil
	}
	path := f.historyStorePath()
	if path == "" {
		return errors.New("剪贴板历史数据目录不可用")
	}
	entries, nextID := f.hist.checkpoint()
	saved := historySnapshot{Version: 1, NextID: nextID, Entries: entries}
	data, err := json.Marshal(saved)
	if err != nil {
		return fmt.Errorf("序列化历史失败: %w", err)
	}
	if len(data) > maxHistoryStore {
		return fmt.Errorf("历史文件超过 %d MiB 上限", maxHistoryStore>>20)
	}
	write := f.writeHistory
	if write == nil {
		write = writeHistoryFile
	}
	return write(path, data)
}

// requestPersist schedules a history write after a short quiet period. Multiple
// clipboard changes inside that window collapse into one full-file rewrite.
func (f *Feature) requestPersist() {
	f.saveMu.Lock()
	defer f.saveMu.Unlock()
	f.savePending = true
	if f.saveTimer != nil {
		f.saveTimer.Stop()
	}
	f.saveTimer = time.AfterFunc(historySaveDelay, func() { f.flushPersist(false) })
}

// cancelScheduledPersist drops any pending write (used when persistence is
// switched off, so the timer cannot recreate the file after deletion).
func (f *Feature) cancelScheduledPersist() {
	f.saveMu.Lock()
	timer := f.saveTimer
	f.saveTimer = nil
	f.savePending = false
	f.saveMu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

// flushPersist writes now when a change is pending (or unconditionally when
// force is set). Stop and startup use force so the on-disk copy always matches
// the in-memory history at lifecycle boundaries.
func (f *Feature) flushPersist(force bool) {
	f.saveMu.Lock()
	timer := f.saveTimer
	f.saveTimer = nil
	pending := f.savePending
	f.savePending = false
	f.saveMu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if !pending && !force {
		return
	}
	if err := f.persistHistory(); err != nil && f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Warn("剪贴板历史持久化失败", "module", moduleID, "err", err)
	}
}

func (f *Feature) removeHistoryStore() error {
	f.cancelScheduledPersist()
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	path := f.historyStorePath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除历史文件失败: %w", err)
	}
	return nil
}

func writeHistoryFile(path string, data []byte) error {
	return writeHistoryFileWith(path, data, os.Rename)
}

// writeHistoryFileWith takes the rename implementation as a parameter so the
// Windows fallback path can be tested without mutating global state.
func writeHistoryFileWith(path string, data []byte, rename func(string, string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建历史目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".clipboard-history-*.tmp")
	if err != nil {
		return fmt.Errorf("创建历史临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("设置历史文件权限失败: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入历史临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步历史临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭历史临时文件失败: %w", err)
	}
	if err := rename(tmpPath, path); err != nil {
		// Windows 上目标文件可能被扫描器/同步盘短暂占用；直接覆盖写比
		// 丢掉本次历史更新更好（与 config.Manager.Save 的策略一致）。
		if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
			return fmt.Errorf("替换历史文件失败: %w（回退直写失败: %v）", err, writeErr)
		}
	}
	return nil
}
