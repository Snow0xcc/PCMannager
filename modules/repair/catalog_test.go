package repair

import (
	"testing"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// TestCatalogCoversRequiredToolbox asserts the user's requested tools are all
// present and reachable. This is the contract behind "电脑修复" feature: every
// named category from the spec must show up in the catalogue.
func TestCatalogCoversRequiredToolbox(t *testing.T) {
	want := []string{
		"flush_dns", "winsock_reset", "tcpip_reset", // 网络排查
		"disk_cleanup", "clear_temp", "clear_recycle", // 系统清理
		"install_chrome", "install_firefox", "install_edge", // 浏览器
		"install_huorong", "install_malwarebytes", // 安全软件
		"install_git", "install_node", "install_python", // git/node/python
		"install_vscode", "install_sublime", // vscode/sublime
		"install_dotnet_runtime", "install_dotnet_sdk", // .NET
		"wsl_install", "wsl_install_ubuntu", "wsl_default_v2", // wsl/wsl2
		"install_appinstaller", "install_choco", // winget
		"install_wireshark", "install_nmap", "install_putty", "install_winscp", // 网络诊断工具
	}
	ids := map[string]bool{}
	for _, e := range Catalog() {
		ids[e.ID] = true
	}
	for _, id := range want {
		if !ids[id] {
			t.Errorf("工具箱缺少必备条目: %s", id)
		}
	}
}

// TestCatalogUniqueIDs guards against two entries colliding on the same action
// id, which would make RunAction ambiguous.
func TestCatalogUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalog() {
		if seen[e.ID] {
			t.Fatalf("重复条目 ID: %s", e.ID)
		}
		seen[e.ID] = true
	}
}

// TestKindClassification checks the presentation hint matches the entry type.
func TestKindClassification(t *testing.T) {
	for _, e := range Catalog() {
		switch {
		case e.Kind() == core.ActionInstall && !e.IsPackage():
			t.Errorf("%s: ActionInstall 但非包条目", e.ID)
		case e.Kind() == core.ActionDanger && !e.Danger:
			t.Errorf("%s: ActionDanger 但 Danger=false", e.ID)
		case e.Kind() == core.ActionNormal && (e.Danger || e.IsPackage()):
			t.Errorf("%s: ActionNormal 但实为危险/包条目", e.ID)
		}
	}
}

// TestResolveCommandWinget builds the expected install line for a package entry.
func TestResolveCommandWinget(t *testing.T) {
	e := Entry{ID: "install_git", WingetID: "Git.Git", ChocoID: "git"}
	cmd, ok := e.ResolveCommand("auto")
	if !ok {
		t.Fatal("auto 模式下应解析出命令")
	}
	if cmd != "winget install --accept-package-agreements --accept-source-agreements Git.Git" {
		t.Fatalf("命令 = %q", cmd)
	}

	// An entry that ONLY has a winget id must still resolve under the choco
	// preference (graceful fallback to the one available manager).
	wingetOnly := Entry{ID: "install_huorong", WingetID: "Huorong.Security"}
	cmd, ok = wingetOnly.ResolveCommand("choco")
	if !ok || cmd != "winget install --accept-package-agreements --accept-source-agreements Huorong.Security" {
		t.Fatalf("choco 回退失败: ok=%v cmd=%q", ok, cmd)
	}
}

// TestResolveCommandChoco prefers chocolatey when both ids exist.
func TestResolveCommandChoco(t *testing.T) {
	e := Entry{ID: "install_chrome", WingetID: "Google.Chrome", ChocoID: "googlechrome"}
	cmd, ok := e.ResolveCommand("choco")
	if !ok {
		t.Fatal("choco 模式应解析出命令")
	}
	if cmd != "choco install googlechrome -y" {
		t.Fatalf("命令 = %q", cmd)
	}
}

// TestResolveCommandLiteral returns the fixed command line for non-package entries.
func TestResolveCommandLiteral(t *testing.T) {
	e := Entry{ID: "flush_dns", Command: "ipconfig /flushdns"}
	cmd, ok := e.ResolveCommand("auto")
	if !ok || cmd != "ipconfig /flushdns" {
		t.Fatalf("字面命令解析失败: ok=%v cmd=%q", ok, cmd)
	}
}

// TestDangerEntriesAreFlagged confirms destructive ops carry Danger so the UI
// can gate them behind a confirm dialog.
func TestDangerEntriesAreFlagged(t *testing.T) {
	danger := []string{
		"hibernate_off", "ip_release", "winsock_reset", "tcpip_reset",
		"clear_temp", "clear_recycle", "wsl_enable_feature", "install_choco",
	}
	for _, id := range danger {
		e, ok := Lookup(id)
		if !ok {
			t.Fatalf("危险条目缺失: %s", id)
		}
		if !e.Danger {
			t.Errorf("%s 应标记为危险", id)
		}
	}
}

// TestPagesOrderIsStable ensures the tab order the walk panel renders from is
// deterministic and starts with the most-used page.
func TestPagesOrderIsStable(t *testing.T) {
	if len(Pages) == 0 {
		t.Fatal("目录应有分页")
	}
	if Pages[0] != "Windows 设置" {
		t.Fatalf("首页应为 Windows 设置，实际 %q", Pages[0])
	}
	// No duplicate pages.
	seen := map[string]bool{}
	for _, p := range Pages {
		if seen[p] {
			t.Fatalf("重复分页: %s", p)
		}
		seen[p] = true
	}
}

// TestGroupsInReturnsDeclaredOrder checks sub-categories appear in the order
// they were declared within a page.
func TestGroupsInReturnsDeclaredOrder(t *testing.T) {
	groups := GroupsIn("开发工具")
	// The declared order is 运行环境, 编辑器 / IDE, WSL, 包管理器 / 基础.
	want := []string{"运行环境", "编辑器 / IDE", "WSL", "包管理器 / 基础"}
	if len(groups) != len(want) {
		t.Fatalf("分组数 = %d, 期望 %d (%v)", len(groups), len(want), groups)
	}
	for i := range want {
		if groups[i] != want[i] {
			t.Fatalf("分组[%d] = %q, 期望 %q", i, groups[i], want[i])
		}
	}
}

// TestLookupMiss returns a clear negative result.
func TestLookupMiss(t *testing.T) {
	if _, ok := Lookup("nope-not-real"); ok {
		t.Fatal("未声明条目不应被找到")
	}
}

// TestEntriesInFiltersByPageAndGroup isolates one sub-category correctly.
func TestEntriesInFiltersByPageAndGroup(t *testing.T) {
	got := EntriesIn("浏览器", "常用浏览器")
	for _, e := range got {
		if e.Page != "浏览器" || e.Group != "常用浏览器" {
			t.Fatalf("过滤错误: %+v", e)
		}
	}
	if len(got) == 0 {
		t.Fatal("应至少返回一个浏览器条目")
	}
}
