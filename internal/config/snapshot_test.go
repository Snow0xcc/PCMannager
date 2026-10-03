package config

import (
	"sync"
	"testing"
)

// TestConfigSnapshotIsolation（A7）：Config() 不得外泄活文档指针——
// 调用方拿着返回值做任何修改都不应影响 Manager 内部状态；
// 反之 Manager 的写入也不应被调用方旧的句柄观察到半成品状态。
func TestConfigSnapshotIsolation(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	snap := m.Config()
	snap.App.Autostart = true
	snap.App.ServerPort = 12345
	if mod, ok := snap.Modules["taskbar"]; ok {
		mod.Enabled = false
		snap.Modules["taskbar"] = mod
	}

	if m.App().Autostart {
		t.Error("修改快照影响了 Manager：Config() 泄露了活文档指针")
	}
	if m.App().ServerPort == 12345 {
		t.Error("修改快照影响了 Manager 的 ServerPort")
	}
	if !m.Module("taskbar").Enabled() {
		t.Error("修改快照影响了 Manager 的模块开关")
	}
}

// TestConfigSnapshotConsistentUnderWrite（A7）：快照必须在锁内整体拷贝，
// 与 UpdateApp 并发时不出现撕裂读。
func TestConfigSnapshotConsistentUnderWrite(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const writers = 4
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = m.UpdateApp(func(c *App) { c.ServerPort++ })
			}
		}()
	}
	// 读方：反复取快照，ServerPort 必须是非负整数（不允许撕裂/半写）。
	for i := 0; i < 500; i++ {
		if p := m.Config().App.ServerPort; p < 0 {
			t.Fatalf("读到负值端口 %d：快照与写入并发不安全", p)
		}
	}
	close(stop)
	wg.Wait()
}
