package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsExecutablePath 守护可执行/脚本判定：.exe/.bat/.cmd/.ps1/.lnk 归为可执行，
// 其它扩展名（含大小写）不算。
func TestIsExecutablePath(t *testing.T) {
	exec := []string{"a.exe", "b.BAT", "c.cmd", "d.Ps1", "e.LNK"}
	for _, p := range exec {
		if !isExecutablePath(p) {
			t.Errorf("isExecutablePath(%q) 应为 true", p)
		}
	}
	plain := []string{"readme.txt", "photo.png", "data.json", "doc.pdf", ""}
	for _, p := range plain {
		if isExecutablePath(p) {
			t.Errorf("isExecutablePath(%q) 应为 false", p)
		}
	}
}

// TestCommandKeyPathKinds 守护本地目标的 key：app/file/folder 都映射到
// path:<路径>，与打开次数/置顶的持久化 key 一致。
func TestCommandKeyPathKinds(t *testing.T) {
	cases := []struct {
		c    command
		want string
	}{
		{command{Kind: "app", Path: "C:\\x\\a.lnk"}, "path:C:\\x\\a.lnk"},
		{command{Kind: "file", Path: "/tmp/doc.txt"}, "path:/tmp/doc.txt"},
		{command{Kind: "folder", Path: "D:\\dir"}, "path:D:\\dir"},
	}
	for _, c := range cases {
		if got := c.c.key(); got != c.want {
			t.Errorf("key() = %q, 期望 %q", got, c.want)
		}
	}
}

// TestScanLnkDirs 守护扫描语义：只有 .lnk 被收进候选（其它文件/子目录跳过），
// 返回项带 app 语义、Label 去扩展名、按 Label 升序稳定排序。
func TestScanLnkDirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{
		filepath.Join(dir, "记事本.lnk"),
		filepath.Join(dir, "readme.txt"), // 非 lnk：跳过
		filepath.Join(sub, "Alpha App.lnk"),
		filepath.Join(sub, "Zed Tool.lnk"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := scanLnkDirs([]string{dir})
	if len(got) != 3 {
		t.Fatalf("应只收 3 个 .lnk, 实际 %d: %+v", len(got), got)
	}
	// 排序按 Label 升序：Alpha App < Zed Tool < 记事本（Go 字符串序，中文按码位靠后）。
	if got[0].Label != "Alpha App" || got[1].Label != "Zed Tool" || got[2].Label != "记事本" {
		t.Fatalf("排序不符: %q %q %q", got[0].Label, got[1].Label, got[2].Label)
	}
	for _, c := range got {
		if c.Kind != "app" || c.Icon != "app" || c.Priority != 5 || c.Path == "" {
			t.Errorf("候选字段不完整: %+v", c)
		}
	}
	// Label 必须去掉了 .lnk 扩展名。
	if got[2].Path != files[0] {
		t.Errorf("路径不符: %q, 期望 %q", got[2].Path, files[0])
	}
}

// TestScanLnkDirsEmptyRoot 守护空/不存在的根：静默返回空，不报错。
func TestScanLnkDirsEmptyRoot(t *testing.T) {
	got := scanLnkDirs([]string{filepath.Join(t.TempDir(), "不存在")})
	if len(got) != 0 {
		t.Fatalf("不存在的根应返回空, 实际 %d", len(got))
	}
}
