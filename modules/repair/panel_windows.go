//go:build windows

package pcrepair

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// openPanel builds the PC-repair / tool-installation window. It groups every
// common Windows repair action and one-click installer under clear categories.
func openPanel(f *Feature) {
	var logEdit *walk.TextEdit

	// action pairs a button label with the command line to execute.
	type action struct {
		name    string
		cmdline string
	}

	// group renders a titled GroupBox of buttons for one sub-category.
	group := func(title string, actions []action) GroupBox {
		buttons := make([]Widget, 0, len(actions))
		for _, a := range actions {
			a := a
			buttons = append(buttons, PushButton{
				Text: a.name,
				OnClicked: func() {
					runAction(f, logEdit, a.name, a.cmdline)
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
			Title:   title,
			Layout:  VBox{},
			Children: children,
		}
	}

	// winget is a reusable installer command line builder.
	wi := func(id string) string {
		return "winget install --accept-package-agreements --accept-source-agreements " + id
	}

	pSettings := page("Windows 设置",
		group("系统与设置", []action{
			{"打开设置", "start ms-settings:"},
			{"控制面板", "control"},
			{"系统信息", "msinfo32"},
			{"设备管理器", "devmgmt.msc"},
			{"磁盘管理", "diskmgmt.msc"},
			{"服务", "services.msc"},
			{"任务计划", "taskschd.msc"},
			{"电源选项", "powercfg.cpl"},
		}),
		group("常用修复", []action{
			{"关闭休眠", "powercfg -h off"},
			{"开启休眠", "powercfg -h on"},
			{"显示隐藏文件", `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 1 /f`},
			{"隐藏隐藏文件", `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v Hidden /t REG_DWORD /d 2 /f`},
			{"显示文件扩展名", `reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" /v HideFileExt /t REG_DWORD /d 0 /f`},
			{"重建图标缓存", `ie4uinit.exe -show`},
		}),
	)

	pNetwork := page("网络排查",
		group("常见修复", []action{
			{"刷新 DNS", "ipconfig /flushdns"},
			{"释放 IP", "ipconfig /release"},
			{"续订 IP", "ipconfig /renew"},
			{"重置 Winsock", "netsh winsock reset"},
			{"重置 TCP/IP", "netsh int ip reset"},
			{"释放/续订+刷新(一键)", "ipconfig /release && ipconfig /renew && ipconfig /flushdns"},
		}),
		group("诊断工具", []action{
			{"Ping 8.8.8.8", "ping -n 4 8.8.8.8"},
			{"Tracert 路由追踪", "tracert -d 8.8.8.8"},
			{"Nslookup 域名解析", "nslookup google.com"},
			{"查看端口占用", "netstat -ano"},
			{"网络重置", "netsh advfirewall reset"},
			{"查看 IP 配置", "ipconfig /all"},
		}),
	)

	pCleanup := page("系统清理",
		group("清理", []action{
			{"磁盘清理", "cleanmgr /sagerun:1"},
			{"清空 Temp", "del /q/f/s %TEMP%\\*"},
			{"清空回收站", `powershell -NoProfile -Command "Clear-RecycleBin -Force"`},
			{"清理 Windows 更新缓存", "net stop wuauserv && del /q/f/s %windir%\\SoftwareDistribution\\* && net start wuauserv"},
			{"磁盘碎片/优化", "dfrgui"},
			{"关闭休眠文件释放空间", "powercfg -h off"},
		}),
	)

	pBrowser := page("浏览器",
		group("常用浏览器", []action{
			{"Google Chrome", wi("Google.Chrome")},
			{"Mozilla Firefox", wi("Mozilla.Firefox")},
			{"Microsoft Edge", wi("Microsoft.Edge")},
			{"Opera", wi("Opera.Opera")},
			{"Brave", wi("Brave.Brave")},
		}),
	)

	pSecurity := page("安全软件",
		group("安全/防护", []action{
			{"火绒安全", wi("Huorong.Security")},
			{"Malwarebytes", wi("Malwarebytes.Malwarebytes")},
			{"Bitdefender", wi("Bitdefender.BitdefenderAntivirusFree")},
			{"Kaspersky", wi("Kaspersky.KasperskyFree")},
			{"启动 Windows Defender 快速扫描", "powershell -NoProfile -Command \"Start-MpScan -ScanType QuickScan\""},
		}),
	)

	pDev := page("开发工具",
		group("运行环境", []action{
			{"Git", wi("Git.Git")},
			{"Node.js", wi("OpenJS.NodeJS.LTS")},
			{"Python 3", wi("Python.Python.3.12")},
			{".NET Runtime", wi("Microsoft.DotNet.Runtime")},
			{".NET SDK", wi("Microsoft.DotNet.SDK")},
			{"OpenJDK (Temurin)", wi("EclipseAdoptium.Temurin.17")},
		}),
		group("编辑器 / IDE", []action{
			{"VS Code", wi("Microsoft.VisualStudioCode")},
			{"Sublime Text", wi("SublimeHQ.SublimeText.4")},
			{"Notepad++", wi("Notepad++.Notepad++")},
			{"JetBrains Toolbox", wi("JetBrains.Toolbox")},
		}),
		group("WSL", []action{
			{"安装 WSL (默认 WSL2)", "wsl --install"},
			{"安装/升级到 WSL2", "wsl --install -d Ubuntu"},
			{"设置默认版本为 WSL2", "wsl --set-default-version 2"},
			{"启用 WSL 可选功能", `dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart`},
			{"启用虚拟机平台( WSL2 依赖)", `dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart`},
			{"通过 winget 安装 WSL", wi("Microsoft.WSL")},
		}),
		group("包管理器 / 基础", []action{
			{"App Installer (winget)", wi("Microsoft.AppInstaller")},
			{"Chocolatey", "powershell -NoProfile -Command \"Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://community.chocolatey.org/install.ps1'))\""},
		}),
	)

	pNetTools := page("网络诊断工具",
		group("抓包 / 探测", []action{
			{"Wireshark", wi("WiresharkFoundation.Wireshark")},
			{"Nmap", wi("Insecure.Nmap")},
			{"TCPView", wi("Sysinternals.TCPView")},
			{"WinMTR", wi("AppleBloom.WinMTR")},
			{"PuTTY", wi("PuTTY.PuTTY")},
			{"MobaXterm", wi("Mobatek.MobaXterm")},
			{"curl", wi("cURL.cURL")},
			{"WinSCP", wi("WinSCP.WinSCP")},
		}),
	)

	MainWindow{
		Title:   "PCMannager - 电脑修复与工具安装",
		MinSize: Size{Width: 720, Height: 600},
		Layout:  VBox{},
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
	}.Run()
}

// runAction executes a cmdline via cmd /c and appends the captured output to the log.
func runAction(f *Feature, logEdit *walk.TextEdit, name, cmdline string) {
	if f.app != nil {
		f.app.Log.Infof("[pcrepair] %s: %s", name, cmdline)
	}
	var buf bytes.Buffer
	cmd := exec.Command("cmd", "/c", cmdline)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if logEdit != nil {
		line := fmt.Sprintf("=== %s ===\n%s", name, buf.String())
		if err != nil {
			line += fmt.Sprintf("[error] %v\n", err)
		}
		if cur := logEdit.Text(); cur == "" {
			logEdit.SetText(line)
		} else {
			logEdit.SetText(cur + "\n" + line)
		}
	}
}
