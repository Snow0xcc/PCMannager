//go:build windows

package repair

import (
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// repairPanelTitle is also used to find an already-open panel window.
const repairPanelTitle = "PCMannager - 电脑修复与工具安装"

// panelState tracks the single open repair window so OpenUI can focus it
// instead of spawning duplicates, and Stop can close it. The pointer is read
// from the UI goroutine and written from OpenUI/Stop, hence the mutex.
var panelState struct {
	mu sync.Mutex
	mw *walk.MainWindow
}

// setPanel records the currently open panel window.
func setPanel(mw *walk.MainWindow) {
	panelState.mu.Lock()
	defer panelState.mu.Unlock()
	panelState.mw = mw
}

// currentPanel returns the open panel window, if any.
func currentPanel() *walk.MainWindow {
	panelState.mu.Lock()
	defer panelState.mu.Unlock()
	return panelState.mw
}

// openPanel builds the PC-repair / tool-installation window. It groups every
// common Windows repair action and one-click installer under clear categories.
func openPanel(f *Feature) {
	var mw *walk.MainWindow
	var logEdit *walk.TextEdit

	// action pairs a button label with the command line to execute.
	type action struct {
		name    string
		cmdline string
		danger  bool
	}

	// group renders a titled GroupBox of buttons for one sub-category.
	group := func(title string, actions []action) GroupBox {
		buttons := make([]Widget, 0, len(actions))
		for _, a := range actions {
			a := a
			buttons = append(buttons, PushButton{
				Text: a.name,
				OnClicked: func() {
					runAction(f, logEdit, a.name, a.cmdline, a.danger)
				},
			})
		}
		return GroupBox{
			Title:  title,
			Layout: VBox{},
			Children: []Widget{
				Composite{Layout: HBox{}, Children: buttons},
			},
		}
	}

	// page bundles several groups into a tab page.
	page := func(title string, groups ...GroupBox) TabPage {
		children := make([]Widget, 0, len(groups))
		for _, g := range groups {
			g := g
			children = append(children, g)
		}
		return TabPage{
			Title:    title,
			Layout:   VBox{},
			Children: children,
		}
	}

	// winget is a reusable installer command line builder.
	wi := func(id string) string {
		return "winget install --accept-package-agreements --accept-source-agreements " + id
	}

	pSettings := page("Windows 设置",
		group("系统与设置", []action{
			{"打开设置", "start ms-settings:", false},
			{"控制面板", "control", false},
			{"系统信息", "msinfo32", false},
			{"设备管理器", "devmgmt.msc", false},
			{"磁盘管理", "diskmgmt.msc", false},
			{"服务", "services.msc", false},
			{"任务计划", "taskschd.msc", false},
			{"电源选项", "powercfg.cpl", false},
		}),
		group("常用修复", []action{
			{"关闭休眠", "powercfg -h off", true},
			{"开启休眠", "powercfg -h on", false},
			{"显示隐藏文件", `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 1 /f`, true},
			{"隐藏隐藏文件", `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 2 /f`, true},
			{"显示文件扩展名", `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v HideFileExt /t REG_DWORD /d 0 /f`, true},
			{"重建图标缓存", `ie4uinit.exe -show`, false},
		}),
	)

	pNetwork := page("网络排查",
		group("常见修复", []action{
			{"刷新 DNS", "ipconfig /flushdns", false},
			{"释放 IP", "ipconfig /release", true},
			{"续订 IP", "ipconfig /renew", false},
			{"重置 Winsock", "netsh winsock reset", true},
			{"重置 TCP/IP", "netsh int ip reset", true},
			{"释放/续订+刷新(一键)", "ipconfig /release && ipconfig /renew && ipconfig /flushdns", true},
		}),
		group("诊断工具", []action{
			{"Ping 8.8.8.8", "ping -n 4 8.8.8.8", false},
			{"Tracert 路由追踪", "tracert -d 8.8.8.8", false},
			{"Nslookup 域名解析", "nslookup google.com", false},
			{"查看端口占用", "netstat -ano", false},
			{"网络重置", "netsh advfirewall reset", true},
			{"查看 IP 配置", "ipconfig /all", false},
		}),
	)

	pCleanup := page("系统清理",
		group("清理", []action{
			{"磁盘清理", "cleanmgr /sagerun:1", false},
			{"清空 Temp", "del /q/f/s %TEMP%\\*", true},
			{"清空回收站", `powershell -NoProfile -Command "Clear-RecycleBin -Force"`, true},
			{"清理 Windows 更新缓存", "net stop wuauserv && del /q/f/s %windir%\\SoftwareDistribution\\* && net start wuauserv", true},
			{"磁盘碎片/优化", "dfrgui", false},
			{"关闭休眠文件释放空间", "powercfg -h off", true},
		}),
	)

	pBrowser := page("浏览器",
		group("常用浏览器", []action{
			{"Google Chrome", wi("Google.Chrome"), false},
			{"Mozilla Firefox", wi("Mozilla.Firefox"), false},
			{"Microsoft Edge", wi("Microsoft.Edge"), false},
			{"Opera", wi("Opera.Opera"), false},
			{"Brave", wi("Brave.Brave"), false},
		}),
	)

	pSecurity := page("安全软件",
		group("安全/防护", []action{
			{"火绒安全", wi("Huorong.Security"), false},
			{"Malwarebytes", wi("Malwarebytes.Malwarebytes"), false},
			{"Bitdefender", wi("Bitdefender.BitdefenderAntivirusFree"), false},
			{"Kaspersky", wi("Kaspersky.KasperskyFree"), false},
			{"启动 Windows Defender 快速扫描", `powershell -NoProfile -Command "Start-MpScan -ScanType QuickScan"`, false},
		}),
	)

	pDev := page("开发工具",
		group("运行环境", []action{
			{"Git", wi("Git.Git"), false},
			{"Node.js", wi("OpenJS.NodeJS.LTS"), false},
			{"Python 3", wi("Python.Python.3.12"), false},
			{".NET Runtime", wi("Microsoft.DotNet.Runtime"), false},
			{".NET SDK", wi("Microsoft.DotNet.SDK"), false},
			{"OpenJDK (Temurin)", wi("EclipseAdoptium.Temurin.17"), false},
		}),
		group("编辑器 / IDE", []action{
			{"VS Code", wi("Microsoft.VisualStudioCode"), false},
			{"Sublime Text", wi("SublimeHQ.SublimeText.4"), false},
			{"Notepad++", wi("Notepad++.Notepad++"), false},
			{"JetBrains Toolbox", wi("JetBrains.Toolbox"), false},
		}),
		group("WSL", []action{
			{"安装 WSL (默认 WSL2)", "wsl --install", false},
			{"安装/升级到 WSL2", "wsl --install -d Ubuntu", false},
			{"设置默认版本为 WSL2", "wsl --set-default-version 2", false},
			{"启用 WSL 可选功能", `dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart`, true},
			{"启用虚拟机平台( WSL2 依赖)", `dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart`, true},
			{"通过 winget 安装 WSL", wi("Microsoft.WSL"), false},
		}),
		group("包管理器 / 基础", []action{
			{"App Installer (winget)", wi("Microsoft.AppInstaller"), false},
			{"Chocolatey", `powershell -NoProfile -Command "Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://community.chocolatey.org/install.ps1'))"`, true},
		}),
	)

	pNetTools := page("网络诊断工具",
		group("抓包 / 探测", []action{
			{"Wireshark", wi("WiresharkFoundation.Wireshark"), false},
			{"Nmap", wi("Insecure.Nmap"), false},
			{"TCPView", wi("Sysinternals.TCPView"), false},
			{"WinMTR", wi("AppleBloom.WinMTR"), false},
			{"PuTTY", wi("PuTTY.PuTTY"), false},
			{"MobaXterm", wi("Mobatek.MobaXterm"), false},
			{"curl", wi("cURL.cURL"), false},
			{"WinSCP", wi("WinSCP.WinSCP"), false},
		}),
	)

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    repairPanelTitle,
		MinSize:  Size{Width: 720, Height: 600},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{pSettings, pNetwork, pCleanup, pBrowser, pSecurity, pDev, pNetTools},
			},
			Label{Text: "运行日志:"},
			TextEdit{
				AssignTo: &logEdit,
				ReadOnly: true,
				MinSize:  Size{Height: 140},
				VScroll:  true,
			},
		},
	}).Create(); err != nil {
		if f.ctx != nil && f.ctx.Logger != nil {
			f.ctx.Logger.Error("创建修复面板失败", "module", moduleID, "err", err)
		}
		return
	}

	setPanel(mw)
	mw.Run()
	setPanel(nil)
}

// focusPanel brings an already-open panel to the front.
func (f *Feature) focusPanel() { focusRepairPanel() }

// closePanel closes the panel when the module stops.
func (f *Feature) closePanel() { closeRepairPanel() }

// focusRepairPanel brings an already-open panel to the front.
func focusRepairPanel() {
	mw := currentPanel()
	if mw == nil {
		return
	}
	_ = mw.BringToTop()
	mw.SetVisible(true)
}

// closeRepairPanel asks the panel to close and releases the handle.
//
// The request is posted asynchronously: Stop() runs while holding the feature
// mutex, and a synchronous SendMessage to a message loop that is shutting down
// could deadlock the whole application.
func closeRepairPanel() {
	mw := currentPanel()
	if mw == nil {
		return
	}
	go func() { _ = mw.Close() }()
}

// runAction executes a cmdline, confirming dangerous ones first, and appends
// the captured output to the panel log.
func runAction(f *Feature, logEdit *walk.TextEdit, name, cmdline string, danger bool) {
	if danger && f != nil && f.confirmDanger() && !confirm(f, name) {
		return
	}
	out, err := runCommand(cmdline)
	if f != nil && f.ctx != nil {
		if f.ctx.Logger != nil {
			f.ctx.Logger.Info("repair 执行动作", "module", moduleID, "action", name, "cmd", cmdline)
		}
		if f.ctx.Bus != nil {
			if err != nil {
				f.ctx.Bus.Log(moduleID, "error", name+": "+err.Error())
			} else {
				f.ctx.Bus.Progress(moduleID, name, 100, out)
			}
		}
	}
	if logEdit != nil {
		line := appendLogf(name, out, err)
		if cur := logEdit.Text(); cur == "" {
			_ = logEdit.SetText(line)
		} else {
			_ = logEdit.SetText(cur + "\n" + line)
		}
	}
}

// confirm asks the user before running a dangerous command.
func confirm(f *Feature, name string) bool {
	mw := currentPanel()
	if mw == nil {
		return true
	}
	rc := walk.MsgBox(mw, "确认操作", "确定要执行「"+name+"」吗？", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion)
	if rc == walk.DlgCmdYes && f != nil && f.ctx != nil && f.ctx.Logger != nil {
		f.ctx.Logger.Info("repair 已确认危险操作", "module", moduleID, "action", name)
	}
	return rc == walk.DlgCmdYes
}
