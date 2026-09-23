package repair

import "github.com/snow0xcc/pcmannager/internal/core"

// The repair toolbox is declared as data rather than built inline by a UI
// layer, so every surface (the walk window, the web preferences panel and the
// REST API) renders the same catalogue from one source of truth.
//
// Entries that install software carry package ids instead of a hard-coded
// command line: the concrete command is resolved at run time from the
// prefer_source option, which is what makes winget/chocolatey switchable.

// Entry is one repair command or one-click installer.
type Entry struct {
	// ID is the stable action id used by the API and by RunAction.
	ID string
	// Label is the button caption.
	Label string
	// Page is the tab the entry belongs to.
	Page string
	// Group is the sub-category inside the page.
	Group string
	// Command is a literal command line (mutually exclusive with packages).
	Command string
	// WingetID, when set, builds a `winget install` command.
	WingetID string
	// ChocoID, when set, builds a `choco install` command.
	ChocoID string
	// Danger marks operations that can change system state destructively;
	// they are gated behind confirm_danger.
	Danger bool
	// Admin marks operations that need elevation to succeed.
	Admin bool
	// Install marks software-installation entries (rendered distinctly).
	Install bool
}

// IsPackage reports whether the entry installs a package rather than running a
// fixed command line.
func (e Entry) IsPackage() bool { return e.WingetID != "" || e.ChocoID != "" }

// Kind maps the entry to the panel's presentation hint.
func (e Entry) Kind() core.ActionKind {
	switch {
	case e.WingetID != "" || e.ChocoID != "":
		return core.ActionInstall
	case e.Danger:
		return core.ActionDanger
	default:
		return core.ActionNormal
	}
}

// Page identifiers, so the UI can order tabs deterministically.
const (
	pageSettings = "Windows 设置"
	pageNetwork  = "网络排查"
	pageCleanup  = "系统清理"
	pageBrowser  = "浏览器"
	pageSecurity = "安全软件"
	pageDev      = "开发工具"
	pageNetTools = "网络诊断工具"
)

// Pages lists the catalogue's tabs in display order.
var Pages = []string{
	pageSettings, pageNetwork, pageCleanup,
	pageBrowser, pageSecurity, pageDev, pageNetTools,
}

// catalog is the full toolbox. It is platform-independent data: resolving a
// command for the current OS happens in Command.
var catalog = []Entry{
	// ── Windows 设置 ──────────────────────────────────────────────────────
	{ID: "open_settings", Label: "打开设置", Page: pageSettings, Group: "系统与设置",
		Command: "start ms-settings:"},
	{ID: "open_control_panel", Label: "控制面板", Page: pageSettings, Group: "系统与设置",
		Command: "control"},
	{ID: "open_msinfo", Label: "系统信息", Page: pageSettings, Group: "系统与设置",
		Command: "msinfo32"},
	{ID: "open_devmgmt", Label: "设备管理器", Page: pageSettings, Group: "系统与设置",
		Command: "devmgmt.msc"},
	{ID: "open_diskmgmt", Label: "磁盘管理", Page: pageSettings, Group: "系统与设置",
		Command: "diskmgmt.msc"},
	{ID: "open_services", Label: "服务", Page: pageSettings, Group: "系统与设置",
		Command: "services.msc"},
	{ID: "open_taskschd", Label: "任务计划", Page: pageSettings, Group: "系统与设置",
		Command: "taskschd.msc"},
	{ID: "open_powercfg", Label: "电源选项", Page: pageSettings, Group: "系统与设置",
		Command: "powercfg.cpl"},

	{ID: "hibernate_off", Label: "关闭休眠", Page: pageSettings, Group: "常用修复",
		Command: "powercfg -h off", Danger: true, Admin: true},
	{ID: "hibernate_on", Label: "开启休眠", Page: pageSettings, Group: "常用修复",
		Command: "powercfg -h on", Admin: true},
	{ID: "show_hidden_files", Label: "显示隐藏文件", Page: pageSettings, Group: "常用修复",
		Command: `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 1 /f`,
		Danger:  true},
	{ID: "hide_hidden_files", Label: "隐藏隐藏文件", Page: pageSettings, Group: "常用修复",
		Command: `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 2 /f`,
		Danger:  true},
	{ID: "show_extensions", Label: "显示文件扩展名", Page: pageSettings, Group: "常用修复",
		Command: `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v HideFileExt /t REG_DWORD /d 0 /f`,
		Danger:  true},
	{ID: "rebuild_icon_cache", Label: "重建图标缓存", Page: pageSettings, Group: "常用修复",
		Command: "ie4uinit.exe -show"},

	// ── 网络排查 ──────────────────────────────────────────────────────────
	{ID: "flush_dns", Label: "刷新 DNS", Page: pageNetwork, Group: "常见修复",
		Command: "ipconfig /flushdns", Admin: true},
	{ID: "ip_release", Label: "释放 IP", Page: pageNetwork, Group: "常见修复",
		Command: "ipconfig /release", Danger: true, Admin: true},
	{ID: "ip_renew", Label: "续订 IP", Page: pageNetwork, Group: "常见修复",
		Command: "ipconfig /renew", Admin: true},
	{ID: "winsock_reset", Label: "重置 Winsock", Page: pageNetwork, Group: "常见修复",
		Command: "netsh winsock reset", Danger: true, Admin: true},
	{ID: "tcpip_reset", Label: "重置 TCP/IP", Page: pageNetwork, Group: "常见修复",
		Command: "netsh int ip reset", Danger: true, Admin: true},
	{ID: "network_fix_all", Label: "释放/续订+刷新(一键)", Page: pageNetwork, Group: "常见修复",
		Command: "ipconfig /release && ipconfig /renew && ipconfig /flushdns",
		Danger:  true, Admin: true},

	{ID: "ping_test", Label: "Ping 8.8.8.8", Page: pageNetwork, Group: "诊断工具",
		Command: "ping -n 4 8.8.8.8"},
	{ID: "tracert_test", Label: "Tracert 路由追踪", Page: pageNetwork, Group: "诊断工具",
		Command: "tracert -d 8.8.8.8"},
	{ID: "nslookup_test", Label: "Nslookup 域名解析", Page: pageNetwork, Group: "诊断工具",
		Command: "nslookup google.com"},
	{ID: "netstat_ports", Label: "查看端口占用", Page: pageNetwork, Group: "诊断工具",
		Command: "netstat -ano"},
	{ID: "firewall_reset", Label: "网络重置", Page: pageNetwork, Group: "诊断工具",
		Command: "netsh advfirewall reset", Danger: true, Admin: true},
	{ID: "ipconfig_all", Label: "查看 IP 配置", Page: pageNetwork, Group: "诊断工具",
		Command: "ipconfig /all"},

	// ── 系统清理 ──────────────────────────────────────────────────────────
	{ID: "disk_cleanup", Label: "磁盘清理", Page: pageCleanup, Group: "清理",
		Command: "cleanmgr /sagerun:1"},
	{ID: "clear_temp", Label: "清空 Temp", Page: pageCleanup, Group: "清理",
		Command: "del /q/f/s %TEMP%\\*", Danger: true},
	{ID: "clear_recycle", Label: "清空回收站", Page: pageCleanup, Group: "清理",
		Command: `powershell -NoProfile -Command "Clear-RecycleBin -Force"`, Danger: true},
	{ID: "clear_wu_cache", Label: "清理 Windows 更新缓存", Page: pageCleanup, Group: "清理",
		Command: "net stop wuauserv && del /q/f/s %windir%\\SoftwareDistribution\\* && net start wuauserv",
		Danger:  true, Admin: true},
	{ID: "defrag", Label: "磁盘碎片/优化", Page: pageCleanup, Group: "清理",
		Command: "dfrgui"},
	{ID: "cleanup_hibernate_off", Label: "关闭休眠文件释放空间", Page: pageCleanup, Group: "清理",
		Command: "powercfg -h off", Danger: true, Admin: true},

	// ── 浏览器 ────────────────────────────────────────────────────────────
	{ID: "install_chrome", Label: "Google Chrome", Page: pageBrowser, Group: "常用浏览器",
		WingetID: "Google.Chrome", ChocoID: "googlechrome", Install: true},
	{ID: "install_firefox", Label: "Mozilla Firefox", Page: pageBrowser, Group: "常用浏览器",
		WingetID: "Mozilla.Firefox", ChocoID: "firefox", Install: true},
	{ID: "install_edge", Label: "Microsoft Edge", Page: pageBrowser, Group: "常用浏览器",
		WingetID: "Microsoft.Edge", ChocoID: "microsoft-edge", Install: true},
	{ID: "install_opera", Label: "Opera", Page: pageBrowser, Group: "常用浏览器",
		WingetID: "Opera.Opera", ChocoID: "opera", Install: true},
	{ID: "install_brave", Label: "Brave", Page: pageBrowser, Group: "常用浏览器",
		WingetID: "Brave.Brave", ChocoID: "brave", Install: true},

	// ── 安全软件 ──────────────────────────────────────────────────────────
	{ID: "install_huorong", Label: "火绒安全", Page: pageSecurity, Group: "安全/防护",
		WingetID: "Huorong.Security", Install: true},
	{ID: "install_malwarebytes", Label: "Malwarebytes", Page: pageSecurity, Group: "安全/防护",
		WingetID: "Malwarebytes.Malwarebytes", ChocoID: "malwarebytes", Install: true},
	{ID: "install_bitdefender", Label: "Bitdefender", Page: pageSecurity, Group: "安全/防护",
		WingetID: "Bitdefender.BitdefenderAntivirusFree", Install: true},
	{ID: "install_kaspersky", Label: "Kaspersky", Page: pageSecurity, Group: "安全/防护",
		WingetID: "Kaspersky.KasperskyFree", Install: true},
	{ID: "defender_quick_scan", Label: "启动 Windows Defender 快速扫描", Page: pageSecurity, Group: "安全/防护",
		Command: `powershell -NoProfile -Command "Start-MpScan -ScanType QuickScan"`, Admin: true},

	// ── 开发工具 ──────────────────────────────────────────────────────────
	{ID: "install_git", Label: "Git", Page: pageDev, Group: "运行环境",
		WingetID: "Git.Git", ChocoID: "git", Install: true},
	{ID: "install_node", Label: "Node.js", Page: pageDev, Group: "运行环境",
		WingetID: "OpenJS.NodeJS.LTS", ChocoID: "nodejs-lts", Install: true},
	{ID: "install_python", Label: "Python 3", Page: pageDev, Group: "运行环境",
		WingetID: "Python.Python.3.12", ChocoID: "python", Install: true},
	{ID: "install_dotnet_runtime", Label: ".NET Runtime", Page: pageDev, Group: "运行环境",
		WingetID: "Microsoft.DotNet.Runtime", ChocoID: "dotnet-runtime", Install: true, Admin: true},
	{ID: "install_dotnet_sdk", Label: ".NET SDK", Page: pageDev, Group: "运行环境",
		WingetID: "Microsoft.DotNet.SDK", ChocoID: "dotnet-sdk", Install: true, Admin: true},
	{ID: "install_openjdk", Label: "OpenJDK (Temurin)", Page: pageDev, Group: "运行环境",
		WingetID: "EclipseAdoptium.Temurin.17", ChocoID: "temurin17", Install: true},

	{ID: "install_vscode", Label: "VS Code", Page: pageDev, Group: "编辑器 / IDE",
		WingetID: "Microsoft.VisualStudioCode", ChocoID: "vscode", Install: true},
	{ID: "install_sublime", Label: "Sublime Text", Page: pageDev, Group: "编辑器 / IDE",
		WingetID: "SublimeHQ.SublimeText.4", ChocoID: "sublimetext4", Install: true},
	{ID: "install_notepadpp", Label: "Notepad++", Page: pageDev, Group: "编辑器 / IDE",
		WingetID: "Notepad++.Notepad++", ChocoID: "notepadplusplus", Install: true},
	{ID: "install_jetbrains_toolbox", Label: "JetBrains Toolbox", Page: pageDev, Group: "编辑器 / IDE",
		WingetID: "JetBrains.Toolbox", ChocoID: "jetbrainstoolbox", Install: true},

	{ID: "wsl_install", Label: "安装 WSL (默认 WSL2)", Page: pageDev, Group: "WSL",
		Command: "wsl --install", Admin: true},
	{ID: "wsl_install_ubuntu", Label: "安装/升级到 WSL2", Page: pageDev, Group: "WSL",
		Command: "wsl --install -d Ubuntu", Admin: true},
	{ID: "wsl_default_v2", Label: "设置默认版本为 WSL2", Page: pageDev, Group: "WSL",
		Command: "wsl --set-default-version 2", Admin: true},
	{ID: "wsl_enable_feature", Label: "启用 WSL 可选功能", Page: pageDev, Group: "WSL",
		Command: `dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart`,
		Danger:  true, Admin: true},
	{ID: "wsl_enable_vmplatform", Label: "启用虚拟机平台(WSL2 依赖)", Page: pageDev, Group: "WSL",
		Command: `dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart`,
		Danger:  true, Admin: true},
	{ID: "install_wsl", Label: "通过 winget 安装 WSL", Page: pageDev, Group: "WSL",
		WingetID: "Microsoft.WSL", Install: true},

	{ID: "install_appinstaller", Label: "App Installer (winget)", Page: pageDev, Group: "包管理器 / 基础",
		WingetID: "Microsoft.AppInstaller", Install: true},
	{ID: "install_choco", Label: "Chocolatey", Page: pageDev, Group: "包管理器 / 基础",
		Command: `powershell -NoProfile -Command "Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://community.chocolatey.org/install.ps1'))"`,
		Danger:  true, Admin: true},

	// ── 网络诊断工具 ──────────────────────────────────────────────────────
	{ID: "install_wireshark", Label: "Wireshark", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "WiresharkFoundation.Wireshark", ChocoID: "wireshark", Install: true},
	{ID: "install_nmap", Label: "Nmap", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "Insecure.Nmap", ChocoID: "nmap", Install: true},
	{ID: "install_tcpview", Label: "TCPView", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "Sysinternals.TCPView", ChocoID: "tcpview", Install: true},
	{ID: "install_winmtr", Label: "WinMTR", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "AppleBloom.WinMTR", ChocoID: "winmtr", Install: true},
	{ID: "install_putty", Label: "PuTTY", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "PuTTY.PuTTY", ChocoID: "putty", Install: true},
	{ID: "install_mobaxterm", Label: "MobaXterm", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "Mobatek.MobaXterm", ChocoID: "mobaxterm", Install: true},
	{ID: "install_curl", Label: "curl", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "cURL.cURL", ChocoID: "curl", Install: true},
	{ID: "install_winscp", Label: "WinSCP", Page: pageNetTools, Group: "抓包 / 探测",
		WingetID: "WinSCP.WinSCP", ChocoID: "winscp", Install: true},
}

// Catalog returns every toolbox entry.
func Catalog() []Entry {
	out := make([]Entry, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup finds an entry by action id.
func Lookup(id string) (Entry, bool) {
	for _, e := range catalog {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// GroupsIn returns the distinct sub-categories of a page, in declaration order.
func GroupsIn(page string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range catalog {
		if e.Page != page || seen[e.Group] {
			continue
		}
		seen[e.Group] = true
		out = append(out, e.Group)
	}
	return out
}

// EntriesIn returns the entries of one page/group.
func EntriesIn(page, group string) []Entry {
	var out []Entry
	for _, e := range catalog {
		if e.Page == page && e.Group == group {
			out = append(out, e)
		}
	}
	return out
}

// wingetInstall builds a non-interactive winget install command.
func wingetInstall(id string) string {
	return "winget install --accept-package-agreements --accept-source-agreements " + id
}

// chocoInstall builds a non-interactive chocolatey install command.
func chocoInstall(id string) string {
	return "choco install " + id + " -y"
}

// ResolveCommand resolves the command line for an entry under the given source
// preference ("auto", "winget" or "choco").
//
// It is a method rather than a field named Command because Entry already has a
// literal Command field; the two are mutually exclusive per entry.
//
// Package entries fall back to whichever manager has an id: a tool that only
// exists in winget is still installable when chocolatey is preferred.
func (e Entry) ResolveCommand(source string) (string, bool) {
	if !e.IsPackage() {
		return e.Command, e.Command != ""
	}
	switch source {
	case "choco":
		if e.ChocoID != "" {
			return chocoInstall(e.ChocoID), true
		}
		if e.WingetID != "" {
			return wingetInstall(e.WingetID), true
		}
	case "winget":
		if e.WingetID != "" {
			return wingetInstall(e.WingetID), true
		}
		if e.ChocoID != "" {
			return chocoInstall(e.ChocoID), true
		}
	default: // auto: winget ships with Windows 11 and is the safer default
		if e.WingetID != "" {
			return wingetInstall(e.WingetID), true
		}
		if e.ChocoID != "" {
			return chocoInstall(e.ChocoID), true
		}
	}
	return "", false
}
