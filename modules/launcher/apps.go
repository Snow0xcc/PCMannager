// Package launcher 的应用索引：从系统开始菜单/程序目录扫描 .lnk 快捷方式，
// 作为快捷面板的「本地应用」候选数据源。
//
// 设计取舍：不解析 .lnk 的二进制格式（COM/IShellLink 需要 ole32 与 cgo 之外的
// 笨重封装），而是把 .lnk 的**路径本身**当作可启动目标——Windows 的
// ShellExecute 打开一个 .lnk 就会启动它指向的程序，与直接打开 exe 等价。
// 因此每个 .lnk 即是一个 command{Kind:"app", Path:link, Label:文件名}。
package launcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// startMenuRoots 是要扫描的开始菜单/程序目录（按用户级 → 公共级顺序）。
// 某个根不存在时静默跳过。
func startMenuRoots() []string {
	var roots []string
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		roots = append(roots, filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		roots = append(roots, filepath.Join(programData, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	return roots
}

// appEntries 扫描开始菜单，返回所有 .lnk 快捷方式对应的 command 候选。
// 排序稳定（按 Label 升序），便于测试与首屏展示一致。
func appEntries() []command {
	return scanLnkDirs(startMenuRoots())
}

// scanLnkDirs 扫描给定的目录列表，收集 .lnk 快捷方式为 command 候选。
// 从 appEntries 拆出可注入根的纯函数，供测试构造真实临时目录验证扫描语义。
func scanLnkDirs(roots []string) []command {
	var out []command
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".lnk") {
				return nil
			}
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			out = append(out, command{
				Label: name, Hint: "应用 · 开始菜单",
				Icon: "app", Kind: "app", Path: path, Priority: 5,
			})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// isExecutablePath 判断一个路径是否属于「可执行/脚本」类（.exe/.bat/.cmd/.ps1
// 或 .lnk 快捷方式）。这类目标在右键菜单里出现「以管理员身份运行」项。
func isExecutablePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".bat", ".cmd", ".ps1", ".lnk":
		return true
	}
	return false
}
