//go:build linux

// 本文件在真实 D-Bus 会话总线上集成验证 Linux 托盘。前面 tray_linux_test.go 的
// 单元测试只覆盖纯函数，这里补齐“只有真机才暴露”的那一半：对象导出、信号订阅、
// 向 StatusNotifierWatcher 注册、dbusmenu 往返、角标触发 NewIcon。
//
// 之所以不满足于单元测试：这些环节的错误（接口没导出、属性签名不对、注册没发出去）
// 在单元测试里全部是绿的，只有真正跑一遍会话总线才会暴露。
//
// 关键：必须用 dbus.Connect（= Dial+Auth+Hello）建立连接。dbus.Dial 返回的是
// “私有”连接，不会做认证握手；而 dbus-daemon 在收到 AUTH 之前不处理任何方法调用，
// 于是在未认证连接上发 ListNames/RequestName 会永远等不到响应（表象是挂死）。
// 被测的 tray.Show() 内部走的是 dbus.ConnectSessionBus()，本来就是 Connect 语义。
//
// 跑法：直接 `go test ./internal/tray/` 即可——没有现成总线时会自己拉一个
// dbus-daemon 专用于本测试；开发机没装 dbus 则跳过（PCM_REQUIRE_DBUS=1 时改为失败，
// 让 CI 不出现“绿灯只是因为没执行”）。

package tray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

// connectBus 建立一条可直接使用的会话总线连接（已完成 Auth + Hello）。
func connectBus(t *testing.T, address string) *dbus.Conn {
	t.Helper()
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatalf("连接会话总线 %s 失败: %v", address, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// sessionBusAddress 返回一条可用的会话总线地址。
//
// 优先复用环境里的 DBUS_SESSION_BUS_ADDRESS（桌面环境 / dbus-run-session 会设置）。
// 没有时自己拉一个专用于本测试的 dbus-daemon，与开发机桌面完全隔离。
// 两者都拿不到就跳过；PCM_REQUIRE_DBUS=1 时缺总线直接失败。
func sessionBusAddress(t *testing.T) string {
	t.Helper()

	if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" {
		// 探活：确认环境变量不是陈旧的。
		if conn, err := dbus.Connect(address); err == nil {
			_ = conn.Close()
			return address
		}
		// 环境变量指向的总线不可用，退回自建。
	}
	return startPrivateSessionBus(t)
}

// startPrivateSessionBus 拉起一个独立的 dbus-daemon 会话，返回总线地址。
func startPrivateSessionBus(t *testing.T) string {
	t.Helper()

	// dbus-daemon 来自 dbus 包。开发机没装时跳过以免误伤；CI 必须真的跑到这些
	// 用例——否则“通过”只是因为没执行。
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		if os.Getenv("PCM_REQUIRE_DBUS") == "1" {
			t.Fatalf("CI 要求真实 D-Bus 集成测试，但未找到 dbus-daemon: %v", err)
		}
		t.Skip("未找到 dbus-daemon，跳过真实 D-Bus 集成测试")
	}

	dir := t.TempDir()
	socket := filepath.Join(dir, "bus")
	config := filepath.Join(dir, "session.conf")
	writeSessionConfig(t, config, socket)

	address := "unix:path=" + socket
	// 配置文件必须用 --config-file=FILE 传入；作为位置参数会被 dbus-daemon 当作
	// 未知选项、直接打印 usage 后退出（socket 根本不会创建）。
	cmd := exec.Command(path, "--nofork", "--config-file="+config)
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 dbus-daemon 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// 等总线可用（socket 建立 + 能完成 Auth/Hello）再返回。
	deadline := time.Now().Add(10 * time.Second)
	for {
		if conn, err := dbus.Connect(address); err == nil {
			_ = conn.Close()
			// 关键：把地址写回环境变量。被测的 tray.Show() 内部走的是
			// dbus.ConnectSessionBus()，它只认 DBUS_SESSION_BUS_ADDRESS；不设置的话
			// 会回退去跑 dbus-launch（本环境没装，直接失败）。
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
			return address
		} else if time.Now().After(deadline) {
			t.Fatalf("dbus-daemon 启动超时，地址 %s 连接失败: %v", address, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// writeSessionConfig 写出最小会话配置。
//
// 三个关键点（都是踩过的坑）：
//   - 配置文件必须用 --config-file= 传入，位置参数会被当成未知选项；
//   - 必须声明 <auth>EXTERNAL</auth>；
//   - allow 规则不能把 user 属性和消息属性写在同一条里，dbus 会报
//     “own, user, group and the message-related attributes cannot be combined”。
func writeSessionConfig(t *testing.T, path, socket string) {
	t.Helper()
	config := `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=` + socket + `</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow user="*"/>
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("写入 D-Bus 配置失败: %v", err)
	}
}

// watcherStub 扮演桌面环境里的 org.kde.StatusNotifierWatcher。
type watcherStub struct {
	registered chan string
}

func (w *watcherStub) RegisterStatusNotifierItem(service string) *dbus.Error {
	select {
	case w.registered <- service:
	default:
	}
	return nil
}

func (w *watcherStub) RegisterStatusNotifierHost(service string) *dbus.Error {
	return nil
}

// startWatcher 在总线上占位 watcher 名称并导出注册接口，返回其连接。
func startWatcher(t *testing.T, address string) (*dbus.Conn, *watcherStub) {
	t.Helper()

	conn := connectBus(t, address)

	reply, err := conn.RequestName(statusNotifierWatcher, dbus.NameFlagDoNotQueue)
	if err != nil {
		t.Fatalf("请求 watcher 名称失败: %v", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("未能取得 watcher 名称: %s", reply)
	}

	stub := &watcherStub{registered: make(chan string, 4)}
	if err := conn.Export(stub, statusNotifierPath, statusNotifierWatcher); err != nil {
		t.Fatalf("导出 StatusNotifierWatcher 失败: %v", err)
	}
	if err := conn.Export(
		introspect.NewIntrospectable(watcherIntrospectionNode()),
		statusNotifierPath,
		"org.freedesktop.DBus.Introspectable",
	); err != nil {
		t.Fatalf("导出 watcher 自省接口失败: %v", err)
	}
	return conn, stub
}

// watcherIntrospectionNode 描述 watcher 接口：客户端会用它校验方法与属性名。
func watcherIntrospectionNode() *introspect.Node {
	return &introspect.Node{
		Interfaces: []introspect.Interface{
			{
				Name: statusNotifierWatcher,
				Methods: []introspect.Method{
					{
						Name: "RegisterStatusNotifierItem",
						Args: []introspect.Arg{{Name: "service", Type: "s", Direction: "in"}},
					},
					{
						Name: "RegisterStatusNotifierHost",
						Args: []introspect.Arg{{Name: "service", Type: "s", Direction: "in"}},
					},
				},
				Signals: []introspect.Signal{
					{
						Name: "StatusNotifierItemRegistered",
						Args: []introspect.Arg{{Name: "service", Type: "s"}},
					},
				},
				Properties: []introspect.Property{
					{Name: "RegisteredStatusNotifierItems", Type: "as", Access: "read"},
					{Name: "IsStatusNotifierHostRegistered", Type: "b", Access: "read"},
				},
			},
		},
	}
}

// showLinuxTray 启动托盘并返回其总线名称，失败直接终止测试。
func showLinuxTray(t *testing.T) (*linuxTray, string) {
	t.Helper()

	tray := New(nil, HandlerFunc(func(string) {}), "").(*linuxTray)
	if err := tray.Show(); err != nil {
		t.Fatalf("Show 返回错误: %v", err)
	}
	t.Cleanup(func() { tray.Destroy() })

	tray.mu.Lock()
	name := tray.busName
	tray.mu.Unlock()
	if name == "" {
		t.Fatal("Show 之后没有取得总线名称")
	}
	return tray, name
}

// TestLinuxTrayDBusRegisterWithWatcher 守护注册握手：真实 watcher 必须收到
// RegisterStatusNotifierItem，且总线名称符合规范格式。
func TestLinuxTrayDBusRegisterWithWatcher(t *testing.T) {
	address := sessionBusAddress(t)
	_, watcher := startWatcher(t, address)

	_, busName := showLinuxTray(t)

	select {
	case service := <-watcher.registered:
		if service != busName {
			t.Errorf("watcher 收到的服务名 = %q，期望 %q", service, busName)
		}
		if !strings.HasPrefix(service, "org.freedesktop.StatusNotifierItem-") {
			t.Errorf("服务名 %q 不以 org.freedesktop.StatusNotifierItem- 开头", service)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher 未在超时内收到 RegisterStatusNotifierItem")
	}
}

// TestLinuxTrayDBusProperties 守护 SNI 属性快照：Category/Id/Status/Menu 必须
// 符合规范，Get/Set/GetAll 行为正确。
func TestLinuxTrayDBusProperties(t *testing.T) {
	address := sessionBusAddress(t)
	startWatcher(t, address)
	tray, busName := showLinuxTray(t)

	conn := connectBus(t, address)
	obj := conn.Object(busName, statusNotifierPath)

	var category dbus.Variant
	call := obj.Call(propertiesInterface+".Get", 0, statusNotifierInterface, "Category")
	if err := call.Store(&category); err != nil {
		t.Fatalf("Get Category 失败: %v", err)
	}
	if got := category.Value(); got != "ApplicationStatus" {
		t.Errorf("Category = %v，期望 ApplicationStatus", got)
	}

	var status dbus.Variant
	call = obj.Call(propertiesInterface+".Get", 0, statusNotifierInterface, "Status")
	if err := call.Store(&status); err != nil {
		t.Fatalf("Get Status 失败: %v", err)
	}
	if got := status.Value(); got != "Active" {
		t.Errorf("Status = %v，期望 Active（已显示时）", got)
	}

	// GetAll 必须返回完整快照。
	var all map[string]dbus.Variant
	call = obj.Call(propertiesInterface+".GetAll", 0, statusNotifierInterface)
	if err := call.Store(&all); err != nil {
		t.Fatalf("GetAll 失败: %v", err)
	}
	for _, key := range []string{"Category", "Id", "Status", "IconPixmap", "ToolTip", "Menu"} {
		if _, ok := all[key]; !ok {
			t.Errorf("GetAll 缺少属性 %q", key)
		}
	}
	if id, ok := all["Id"].Value().(string); !ok || id != "pcmannager" {
		t.Errorf("Id = %v，期望 pcmannager", all["Id"].Value())
	}

	// 未知属性必须报 PropertyNotFound，而不是静默返回空。
	call = obj.Call(propertiesInterface+".Get", 0, statusNotifierInterface, "NoSuchProp")
	if call.Err == nil {
		t.Error("Get 未知属性期望报错，实际成功")
	}

	// 只读属性不可写。
	call = obj.Call(propertiesInterface+".Set", 0, statusNotifierInterface, "Status",
		dbus.MakeVariant("Passive"))
	if call.Err == nil {
		t.Error("Set 只读属性期望报错，实际成功")
	}
	// 内部状态不应被这次失败的 Set 改掉。
	tray.mu.Lock()
	visible := tray.visible
	tray.mu.Unlock()
	if !visible {
		t.Error("失败的 Set 之后托盘不应变为不可见")
	}
}

// TestLinuxTrayDBusMenuClick 守护 dbusmenu 往返：GetLayout 返回真实布局，
// Event 触发业务回调。
func TestLinuxTrayDBusMenuClick(t *testing.T) {
	address := sessionBusAddress(t)
	startWatcher(t, address)

	// 手工构造托盘以便捕获点击。
	clicked := make(chan string, 4)
	tray := New(nil, HandlerFunc(func(id string) {
		clicked <- id
	}), "").(*linuxTray)
	tray.SetMenu(Menu{
		Tooltip: "GoBox",
		Items: []Item{
			{ID: "open", Title: "打开面板"},
			{ID: "toggle", Title: "启用", Checkable: true, Checked: true},
		},
	})
	if err := tray.Show(); err != nil {
		t.Fatalf("Show 返回错误: %v", err)
	}
	defer tray.Destroy()

	tray.mu.Lock()
	busName := tray.busName
	tray.mu.Unlock()

	conn := connectBus(t, address)
	menuObj := conn.Object(busName, menuPath)

	var revision uint32
	var layout dbusMenuLayout
	call := menuObj.Call(menuInterface+".GetLayout", 0, int32(0), int32(1), []string{})
	if err := call.Store(&revision, &layout); err != nil {
		t.Fatalf("GetLayout 失败: %v", err)
	}
	if len(layout.Children) != 2 {
		t.Fatalf("菜单子节点数 = %d，期望 2", len(layout.Children))
	}
	// 第一个子节点标签应为“打开面板”。
	// 注意：经总线往返后，子节点不再是 dbusMenuLayout，而是原始的
	// []interface{}（即 (ia{sv}av) 元组的通用形式），需按位取出 label。
	if label := menuChildLabel(layout.Children[0]); label != "打开面板" {
		t.Errorf("首个子节点 label = %q，期望 打开面板", label)
	}

	// 点击第 2 项（编号 2），应触发 toggle 回调。
	call = menuObj.Call(menuInterface+".Event", 0, int32(2), "clicked", dbus.MakeVariant(""), uint32(0))
	if call.Err != nil {
		t.Fatalf("Event 返回错误: %v", call.Err)
	}
	select {
	case id := <-clicked:
		if id != "toggle" {
			t.Errorf("点击回调 ID = %q，期望 toggle", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Event 未触发业务回调")
	}
}

// TestLinuxTrayDBusBadgeEmitsNewIcon 守护角标协议：SetBadge 必须发出 NewIcon
// 与 IconPixmap 的 PropertiesChanged，且 IconPixmap 内容确实随角标变化。
func TestLinuxTrayDBusBadgeEmitsNewIcon(t *testing.T) {
	address := sessionBusAddress(t)
	startWatcher(t, address)
	tray, busName := showLinuxTray(t)

	// 监听属性变化信号。
	conn := connectBus(t, address)
	signals := make(chan *dbus.Signal, 32)
	conn.Signal(signals)
	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(statusNotifierPath),
		dbus.WithMatchInterface(propertiesInterface),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, statusNotifierInterface),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		t.Fatalf("订阅属性变化信号失败: %v", err)
	}

	// 记录改动前图标。
	before := currentIconPixmap(t, conn, busName)

	tray.SetBadge("7")

	// 期望收到带 IconPixmap 的 PropertiesChanged，且图标确实变了。
	changed := waitForIconChange(t, signals, 5*time.Second)
	after := iconPixmapFromVariant(t, changed["IconPixmap"])
	if after == nil {
		t.Fatalf("IconPixmap 为空，期望非空图标数据")
	}
	if sameIconPixmap(before, after) {
		t.Error("SetBadge 后 IconPixmap 与之前相同，角标未生效")
	}

	// 直接读属性也应拿到新图标。
	reloaded := currentIconPixmap(t, conn, busName)
	if sameIconPixmap(before, reloaded) {
		t.Error("SetBadge 后再读 IconPixmap 仍与之前相同，角标未持久化")
	}
}

// menuChildLabel 从一个 dbusmenu 子节点变体里取出 label。
//
// 子节点在总线上是 (ia{sv}av) 结构，解码后表现为
// []interface{}{int32, map[string]dbus.Variant, []dbus.Variant}。
func menuChildLabel(child dbus.Variant) string {
	fields, ok := child.Value().([]any)
	if !ok || len(fields) < 2 {
		return ""
	}
	properties, ok := fields[1].(map[string]dbus.Variant)
	if !ok {
		return ""
	}
	label, ok := properties["label"]
	if !ok {
		return ""
	}
	text, _ := label.Value().(string)
	return text
}

// iconLayer 是从总线上解出的一层图标：尺寸 + ARGB 像素。
type iconLayer struct {
	Width  int
	Height int
	Data   []byte
}

// currentIconPixmap 读取当前 SNI 的 IconPixmap 属性并解成 iconLayer 切片。
func currentIconPixmap(t *testing.T, conn *dbus.Conn, busName string) []iconLayer {
	t.Helper()
	var value dbus.Variant
	call := conn.Object(busName, statusNotifierPath).
		Call(propertiesInterface+".Get", 0, statusNotifierInterface, "IconPixmap")
	if err := call.Store(&value); err != nil {
		t.Fatalf("Get IconPixmap 失败: %v", err)
	}
	layers := iconPixmapFromVariant(t, value)
	if len(layers) == 0 {
		t.Fatal("IconPixmap 期望非空图标数据")
	}
	return layers
}

// iconPixmapFromVariant 把总线上解出的 a(iiay) 转成 []iconLayer。
//
// 经总线往返后结构是 [][]any，每层是 {int32 宽, int32 高, []byte ARGB}。
func iconPixmapFromVariant(t *testing.T, value dbus.Variant) []iconLayer {
	t.Helper()
	layers, ok := value.Value().([][]any)
	if !ok {
		t.Fatalf("IconPixmap 类型 = %T，期望 [][]any", value.Value())
	}
	result := make([]iconLayer, 0, len(layers))
	for _, fields := range layers {
		if len(fields) < 3 {
			t.Fatalf("IconPixmap 层字段数 = %d，期望 >= 3", len(fields))
		}
		width, okWidth := fields[0].(int32)
		height, okHeight := fields[1].(int32)
		data, okData := fields[2].([]byte)
		if !okWidth || !okHeight || !okData {
			t.Fatalf("IconPixmap 层字段类型异常: %T / %T / %T",
				fields[0], fields[1], fields[2])
		}
		result = append(result, iconLayer{
			Width:  int(width),
			Height: int(height),
			Data:   data,
		})
	}
	return result
}

// waitForIconChange 等待带 IconPixmap 的 PropertiesChanged 信号并返回改动表。
func waitForIconChange(t *testing.T, signals <-chan *dbus.Signal, timeout time.Duration) map[string]dbus.Variant {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case signal := <-signals:
			if signal == nil || len(signal.Body) < 2 {
				continue
			}
			changed, ok := signal.Body[1].(map[string]dbus.Variant)
			if !ok {
				continue
			}
			if _, has := changed["IconPixmap"]; has {
				return changed
			}
		case <-deadline:
			t.Fatal("等待 IconPixmap 变化信号超时")
			return nil
		}
	}
}

// sameIconPixmap 逐层比较图标数据是否完全一致。
func sameIconPixmap(a, b []iconLayer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Width != b[i].Width || a[i].Height != b[i].Height {
			return false
		}
		if len(a[i].Data) != len(b[i].Data) {
			return false
		}
		for j := range a[i].Data {
			if a[i].Data[j] != b[i].Data[j] {
				return false
			}
		}
	}
	return true
}
