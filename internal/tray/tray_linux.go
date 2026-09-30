//go:build linux

package tray

import (
	"fmt"
	"image"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/snow0xcc/pcmannager/internal/logo"
)

const (
	statusNotifierInterface = "org.kde.StatusNotifierItem"
	statusNotifierWatcher   = "org.kde.StatusNotifierWatcher"
	statusNotifierPath      = dbus.ObjectPath("/StatusNotifierItem")
	menuInterface           = "com.canonical.dbusmenu"
	menuPath                = dbus.ObjectPath("/MenuBar")
	propertiesInterface     = "org.freedesktop.DBus.Properties"
	introspectableInterface = "org.freedesktop.DBus.Introspectable"
)

var linuxTraySequence atomic.Uint64

// iconPixmap 是 StatusNotifierItem 的 (iiay) 图标结构。
type iconPixmap struct {
	Width  int32
	Height int32
	Data   []byte
}

// statusNotifierToolTip 是 StatusNotifierItem 的 (sa(iiay)ss) 提示结构。
type statusNotifierToolTip struct {
	IconName   string
	IconPixmap []iconPixmap
	Title      string
	Text       string
}

// dbusMenuLayout 是 dbusmenu 的递归 (ia{sv}av) 布局结构。
type dbusMenuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

// linuxTray 管理 Linux 会话总线上的托盘项与菜单对象。
type linuxTray struct {
	log     *slog.Logger
	handler Handler

	// lifecycleMu 串行化连接创建、关闭及连接上的信号发送。
	lifecycleMu sync.Mutex
	mu          sync.Mutex
	menu        Menu
	revision    uint32
	conn        *dbus.Conn
	busName     string
	visible     bool
	destroyed   bool
	icon        []iconPixmap
	badge       string      // 当前角标文本（空 = 无角标）
	baseImg     *image.RGBA // 品牌底图缓存：角标变化时只做合成，不重画几何
}

// statusNotifierItem 只向 SNI 接口导出协议要求的方法。
type statusNotifierItem struct {
	tray *linuxTray
}

// dbusMenu 只向 dbusmenu 接口导出菜单方法。
type dbusMenu struct {
	tray *linuxTray
}

// statusNotifierProperties 实现标准 D-Bus 属性访问接口。
type statusNotifierProperties struct {
	tray *linuxTray
}

// Supported 在 Linux 上为 true：本文件用 StatusNotifierItem 实现了托盘。
//
// 注意这是"构建带实现"，不等于"当前会话一定能显示"——没有 D-Bus 会话总线或
// 没有 StatusNotifierWatcher 时 Show() 会返回错误（有日志），那时面板仍是控制面。
func Supported() bool { return true }

// New 创建一个基于 StatusNotifierItem 的 Linux 系统托盘。
func New(log *slog.Logger, handler Handler) Tray {
	if log == nil {
		log = slog.Default()
	}
	base := logo.Render(64)
	return &linuxTray{
		log:     log,
		handler: handler,
		baseImg: base,
		icon:    rgbaToIconPixmap(base),
	}
}

// SetBadge 更新角标并通知面板刷新图标。
//
// SNI 客户端靠 NewIcon 信号感知图标变化（PropertiesChanged 的 IconPixmap 只是
// 辅助，部分实现只认信号），两者都发。未显示或未连接时只更新缓存，等 Show
// 重新导出时自然带上。
func (t *linuxTray) SetBadge(text string) {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()

	t.mu.Lock()
	if t.badge == text {
		t.mu.Unlock()
		return
	}
	t.badge = text
	if t.baseImg == nil {
		t.baseImg = logo.Render(64)
	}
	t.icon = rgbaToIconPixmap(BadgeOverlay(t.baseImg, text))
	conn, visible := t.conn, t.visible
	t.mu.Unlock()

	if !visible || conn == nil {
		return
	}
	if err := conn.Emit(statusNotifierPath, statusNotifierInterface+".NewIcon"); err != nil {
		t.log.Warn("无法发送托盘图标更新信号", "error", err)
	}
	changed := map[string]dbus.Variant{"IconPixmap": dbus.MakeVariant(t.icon)}
	if err := conn.Emit(statusNotifierPath, propertiesInterface+".PropertiesChanged",
		statusNotifierInterface, changed, []string{}); err != nil {
		t.log.Warn("无法发送托盘图标属性更新信号", "error", err)
	}
}

// SetMenu 替换菜单，并在托盘已显示时通知 dbusmenu 客户端刷新布局。
func (t *linuxTray) SetMenu(menu Menu) {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()

	menu.Items = append([]Item(nil), menu.Items...)

	t.mu.Lock()
	t.menu = menu
	t.revision++
	revision := t.revision
	conn := t.conn
	visible := t.visible
	title := menuTitle(menu.Tooltip)
	t.mu.Unlock()

	if !visible || conn == nil {
		return
	}

	if err := conn.Emit(menuPath, menuInterface+".LayoutUpdated", revision, int32(0)); err != nil {
		t.log.Warn("无法发送托盘菜单布局更新信号", "error", err)
	}
	changed := map[string]dbus.Variant{
		"Title":   dbus.MakeVariant(title),
		"ToolTip": dbus.MakeVariant(statusNotifierToolTip{Title: title}),
	}
	if err := conn.Emit(statusNotifierPath, propertiesInterface+".PropertiesChanged",
		statusNotifierInterface, changed, []string{}); err != nil {
		t.log.Warn("无法发送托盘属性更新信号", "error", err)
	}
	if err := conn.Emit(statusNotifierPath, statusNotifierInterface+".NewTitle"); err != nil {
		t.log.Warn("无法发送托盘标题更新信号", "error", err)
	}
	if err := conn.Emit(statusNotifierPath, statusNotifierInterface+".NewToolTip"); err != nil {
		t.log.Warn("无法发送托盘提示更新信号", "error", err)
	}
}

// Show 连接会话总线、导出对象并向 StatusNotifierWatcher 注册托盘项。
func (t *linuxTray) Show() error {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()

	t.mu.Lock()
	if t.destroyed {
		t.mu.Unlock()
		return fmt.Errorf("Linux 系统托盘已销毁")
	}
	if t.visible {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("无法连接 D-Bus 会话总线: %w", err)
	}

	busName := fmt.Sprintf("org.freedesktop.StatusNotifierItem-%d-%d",
		os.Getpid(), linuxTraySequence.Add(1))
	nameOwned := false
	cleanup := func() {
		if nameOwned {
			_, _ = conn.ReleaseName(busName)
		}
		_ = conn.Close()
	}

	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		cleanup()
		return fmt.Errorf("无法请求托盘 D-Bus 名称 %q: %w", busName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner && reply != dbus.RequestNameReplyAlreadyOwner {
		cleanup()
		return fmt.Errorf("无法取得托盘 D-Bus 名称 %q: %s", busName, reply)
	}
	nameOwned = true

	if err := t.exportObjects(conn); err != nil {
		cleanup()
		return err
	}

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	matchOptions := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, statusNotifierWatcher),
	}
	if err := conn.AddMatchSignal(matchOptions...); err != nil {
		t.log.Warn("无法监听 StatusNotifierWatcher 状态变化", "error", err)
	}

	t.mu.Lock()
	t.conn = conn
	t.busName = busName
	t.visible = true
	t.mu.Unlock()

	go t.listenSignals(conn, signals, busName)
	t.registerWithWatcher(conn, busName)
	return nil
}

// Hide 注销并关闭当前 D-Bus 连接；之后仍可再次调用 Show。
func (t *linuxTray) Hide() {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	t.stopLocked()
}

// Visible 报告托盘项是否已在会话总线上注册。
func (t *linuxTray) Visible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visible
}

// Destroy 永久释放托盘使用的 D-Bus 资源。
func (t *linuxTray) Destroy() {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()

	t.mu.Lock()
	if t.destroyed {
		t.mu.Unlock()
		return
	}
	t.destroyed = true
	t.mu.Unlock()

	t.stopLocked()
}

// stopLocked 在持有 lifecycleMu 时停止当前连接。
func (t *linuxTray) stopLocked() {
	t.mu.Lock()
	conn := t.conn
	busName := t.busName
	t.conn = nil
	t.busName = ""
	t.visible = false
	t.mu.Unlock()

	if conn == nil {
		return
	}

	changed := map[string]dbus.Variant{"Status": dbus.MakeVariant("Passive")}
	if err := conn.Emit(statusNotifierPath, propertiesInterface+".PropertiesChanged",
		statusNotifierInterface, changed, []string{}); err != nil {
		t.log.Warn("无法发送托盘停用信号", "error", err)
	}
	if _, err := conn.ReleaseName(busName); err != nil {
		t.log.Warn("无法释放托盘 D-Bus 名称", "name", busName, "error", err)
	}
	if err := conn.Close(); err != nil {
		t.log.Warn("无法关闭托盘 D-Bus 连接", "error", err)
	}
}

// exportObjects 导出 SNI、属性、dbusmenu 及自省接口。
func (t *linuxTray) exportObjects(conn *dbus.Conn) error {
	if err := conn.Export(&statusNotifierItem{tray: t}, statusNotifierPath, statusNotifierInterface); err != nil {
		return fmt.Errorf("无法导出 StatusNotifierItem 接口: %w", err)
	}
	if err := conn.Export(&statusNotifierProperties{tray: t}, statusNotifierPath, propertiesInterface); err != nil {
		return fmt.Errorf("无法导出 StatusNotifierItem 属性: %w", err)
	}
	if err := conn.Export(&dbusMenu{tray: t}, menuPath, menuInterface); err != nil {
		return fmt.Errorf("无法导出托盘菜单接口: %w", err)
	}
	if err := conn.Export(introspect.NewIntrospectable(statusNotifierIntrospection()),
		statusNotifierPath, introspectableInterface); err != nil {
		return fmt.Errorf("无法导出 StatusNotifierItem 自省接口: %w", err)
	}
	if err := conn.Export(introspect.NewIntrospectable(menuIntrospection()),
		menuPath, introspectableInterface); err != nil {
		return fmt.Errorf("无法导出托盘菜单自省接口: %w", err)
	}
	return nil
}

// registerWithWatcher 注册失败只记录告警，允许无 watcher 的会话继续运行。
func (t *linuxTray) registerWithWatcher(conn *dbus.Conn, busName string) {
	call := conn.Object(statusNotifierWatcher, statusNotifierPath).Call(
		statusNotifierWatcher+".RegisterStatusNotifierItem", 0, busName)
	if call.Err != nil {
		t.log.Warn("无法向 StatusNotifierWatcher 注册托盘项，图标可能不会显示",
			"name", busName, "error", call.Err)
	}
}

// listenSignals 独立处理 watcher 重启信号，并在其重新出现时再次注册。
func (t *linuxTray) listenSignals(conn *dbus.Conn, signals <-chan *dbus.Signal, busName string) {
	for signal := range signals {
		if signal == nil || signal.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(signal.Body) != 3 {
			continue
		}
		name, nameOK := signal.Body[0].(string)
		newOwner, ownerOK := signal.Body[2].(string)
		if !nameOK || !ownerOK || name != statusNotifierWatcher || newOwner == "" {
			continue
		}

		t.mu.Lock()
		active := t.visible && t.conn == conn && t.busName == busName
		t.mu.Unlock()
		if active {
			t.registerWithWatcher(conn, busName)
		}
	}
}

// ProvideXdgActivationToken 接收桌面环境提供的激活令牌；当前无需保存。
func (s *statusNotifierItem) ProvideXdgActivationToken(_ string) *dbus.Error {
	return nil
}

// Scroll 接收托盘滚轮事件；当前菜单没有对应动作。
func (s *statusNotifierItem) Scroll(_ int32, _ string) *dbus.Error {
	return nil
}

// Activate 将主激活动作映射到现有的打开面板命令。
func (s *statusNotifierItem) Activate(_ int32, _ int32) *dbus.Error {
	s.tray.dispatch("open_panel")
	return nil
}

// SecondaryActivate 暂不为次要激活动作分配命令。
func (s *statusNotifierItem) SecondaryActivate(_ int32, _ int32) *dbus.Error {
	return nil
}

// ContextMenu 由桌面环境结合导出的 Menu 属性处理。
func (s *statusNotifierItem) ContextMenu(_ int32, _ int32) *dbus.Error {
	return nil
}

// GetLayout 返回指定节点下的 dbusmenu 布局。
func (m *dbusMenu) GetLayout(parentID int32, recursionDepth int32, propertyNames []string) (
	uint32, dbusMenuLayout, *dbus.Error,
) {
	m.tray.mu.Lock()
	items := append([]Item(nil), m.tray.menu.Items...)
	revision := m.tray.revision
	m.tray.mu.Unlock()

	layout, ok := menuLayoutFor(items, parentID, recursionDepth, propertyNames)
	if !ok {
		return revision, dbusMenuLayout{}, dbus.NewError(
			"com.canonical.dbusmenu.Error.InvalidMenuId",
			[]any{fmt.Sprintf("菜单节点 %d 不存在", parentID)},
		)
	}
	return revision, layout, nil
}

// Event 把 clicked 事件映射回应用层稳定的 Item.ID。
func (m *dbusMenu) Event(id int32, eventID string, _ dbus.Variant, _ uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}

	m.tray.mu.Lock()
	itemID, ok := menuItemID(m.tray.menu.Items, id)
	handler := m.tray.handler
	m.tray.mu.Unlock()

	if ok && handler != nil {
		handler.OnSelect(itemID)
	}
	return nil
}

// AboutToShow 表示菜单无需在显示前另行刷新。
func (m *dbusMenu) AboutToShow(_ int32) (bool, *dbus.Error) {
	return false, nil
}

// Get 返回一个 StatusNotifierItem 属性。
func (p *statusNotifierProperties) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != statusNotifierInterface {
		return dbus.Variant{}, dbus.NewError(
			"org.freedesktop.DBus.Properties.Error.InterfaceNotFound", []any{iface})
	}
	values := p.tray.statusNotifierPropertyValues()
	value, ok := values[property]
	if !ok {
		return dbus.Variant{}, dbus.NewError(
			"org.freedesktop.DBus.Properties.Error.PropertyNotFound", []any{property})
	}
	return dbus.MakeVariant(value), nil
}

// GetAll 返回 StatusNotifierItem 的全部属性。
func (p *statusNotifierProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != statusNotifierInterface {
		return nil, dbus.NewError(
			"org.freedesktop.DBus.Properties.Error.InterfaceNotFound", []any{iface})
	}
	values := p.tray.statusNotifierPropertyValues()
	result := make(map[string]dbus.Variant, len(values))
	for name, value := range values {
		result[name] = dbus.MakeVariant(value)
	}
	return result, nil
}

// Set 拒绝修改只读的 StatusNotifierItem 属性。
func (p *statusNotifierProperties) Set(iface, property string, _ dbus.Variant) *dbus.Error {
	if iface != statusNotifierInterface {
		return dbus.NewError("org.freedesktop.DBus.Properties.Error.InterfaceNotFound", []any{iface})
	}
	if _, ok := p.tray.statusNotifierPropertyValues()[property]; !ok {
		return dbus.NewError("org.freedesktop.DBus.Properties.Error.PropertyNotFound", []any{property})
	}
	return dbus.NewError("org.freedesktop.DBus.Properties.Error.ReadOnly", []any{property})
}

// statusNotifierPropertyValues 生成当前 SNI 属性快照。
func (t *linuxTray) statusNotifierPropertyValues() map[string]any {
	t.mu.Lock()
	title := menuTitle(t.menu.Tooltip)
	status := "Passive"
	if t.visible {
		status = "Active"
	}
	icon := t.icon
	t.mu.Unlock()

	emptyPixmap := []iconPixmap{}
	return map[string]any{
		"Category":            "ApplicationStatus",
		"Id":                  "pcmannager",
		"Title":               title,
		"Status":              status,
		"WindowId":            uint32(0),
		"IconThemePath":       "",
		"IconName":            "",
		"IconPixmap":          icon,
		"OverlayIconName":     "",
		"OverlayIconPixmap":   emptyPixmap,
		"AttentionIconName":   "",
		"AttentionIconPixmap": emptyPixmap,
		"AttentionMovieName":  "",
		"ToolTip":             statusNotifierToolTip{Title: title},
		"ItemIsMenu":          false,
		"Menu":                menuPath,
	}
}

// dispatch 在不持有托盘状态锁时调用业务回调。
func (t *linuxTray) dispatch(id string) {
	t.mu.Lock()
	handler := t.handler
	t.mu.Unlock()
	if handler != nil {
		handler.OnSelect(id)
	}
}

// menuTitle 为未设置提示文字的菜单提供稳定标题。
func menuTitle(tooltip string) string {
	if tooltip == "" {
		return "PCMannager"
	}
	return tooltip
}

// rgbaToIconPixmap 把 RGBA 内存序转换为 SNI 要求的逐像素 A、R、G、B 字节序。
func rgbaToIconPixmap(img *image.RGBA) []iconPixmap {
	if img == nil {
		return []iconPixmap{}
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	data := make([]byte, width*height*4)
	out := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			offset := img.PixOffset(x, y)
			data[out] = img.Pix[offset+3]
			data[out+1] = img.Pix[offset]
			data[out+2] = img.Pix[offset+1]
			data[out+3] = img.Pix[offset+2]
			out += 4
		}
	}
	return []iconPixmap{{Width: int32(width), Height: int32(height), Data: data}}
}

// buildMenuLayout 把扁平 Item 列表构造成根节点为 0 的 dbusmenu 树。
func buildMenuLayout(items []Item) dbusMenuLayout {
	root := dbusMenuLayout{
		ID:         0,
		Properties: map[string]dbus.Variant{},
		Children:   make([]dbus.Variant, 0, len(items)),
	}
	for index, item := range items {
		node := menuNode(item, int32(index+1))
		root.Children = append(root.Children, dbus.MakeVariant(node))
	}
	return root
}

// menuNode 映射单个菜单项；Linux dbusmenu 不支持文字着色，因此忽略 Color。
func menuNode(item Item, id int32) dbusMenuLayout {
	properties := map[string]dbus.Variant{}
	if item.Separator {
		properties["type"] = dbus.MakeVariant("separator")
	} else {
		properties["label"] = dbus.MakeVariant(item.Title)
		properties["enabled"] = dbus.MakeVariant(!item.Disabled)
		if item.Checkable {
			state := int32(0)
			if item.Checked {
				state = 1
			}
			properties["toggle-type"] = dbus.MakeVariant("checkmark")
			properties["toggle-state"] = dbus.MakeVariant(state)
		}
	}
	return dbusMenuLayout{
		ID:         id,
		Properties: properties,
		Children:   []dbus.Variant{},
	}
}

// menuLayoutFor 应用 parent、递归深度和属性过滤条件。
func menuLayoutFor(items []Item, parentID, recursionDepth int32, propertyNames []string) (dbusMenuLayout, bool) {
	var layout dbusMenuLayout
	switch {
	case parentID == 0:
		layout = buildMenuLayout(items)
		if recursionDepth == 0 {
			layout.Children = []dbus.Variant{}
		}
	case parentID > 0 && int(parentID) <= len(items):
		layout = menuNode(items[parentID-1], parentID)
	default:
		return dbusMenuLayout{}, false
	}

	if len(propertyNames) != 0 {
		wanted := make(map[string]struct{}, len(propertyNames))
		for _, name := range propertyNames {
			wanted[name] = struct{}{}
		}
		layout = filterMenuProperties(layout, wanted)
	}
	return layout, true
}

// filterMenuProperties 递归保留调用方请求的菜单属性。
func filterMenuProperties(layout dbusMenuLayout, wanted map[string]struct{}) dbusMenuLayout {
	properties := make(map[string]dbus.Variant, len(layout.Properties))
	for name, value := range layout.Properties {
		if _, ok := wanted[name]; ok {
			properties[name] = value
		}
	}
	layout.Properties = properties
	for index, childValue := range layout.Children {
		child, ok := childValue.Value().(dbusMenuLayout)
		if !ok {
			continue
		}
		layout.Children[index] = dbus.MakeVariant(filterMenuProperties(child, wanted))
	}
	return layout
}

// menuItemID 把 dbusmenu 连续数字编号映射回应用层 ID。
func menuItemID(items []Item, id int32) (string, bool) {
	if id <= 0 || int(id) > len(items) {
		return "", false
	}
	return items[id-1].ID, true
}

// statusNotifierIntrospection 描述 SNI 方法、信号和属性签名。
func statusNotifierIntrospection() *introspect.Node {
	return &introspect.Node{
		Name: string(statusNotifierPath),
		Interfaces: []introspect.Interface{
			{
				Name: statusNotifierInterface,
				Methods: []introspect.Method{
					{Name: "ContextMenu", Args: inArgs("i", "i")},
					{Name: "Activate", Args: inArgs("i", "i")},
					{Name: "SecondaryActivate", Args: inArgs("i", "i")},
					{Name: "Scroll", Args: inArgs("i", "s")},
					{Name: "ProvideXdgActivationToken", Args: inArgs("s")},
				},
				Signals: []introspect.Signal{
					{Name: "NewTitle"},
					{Name: "NewIcon"},
					{Name: "NewAttentionIcon"},
					{Name: "NewOverlayIcon"},
					{Name: "NewToolTip"},
					{Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}},
				},
				Properties: statusNotifierPropertyIntrospection(),
			},
			propertiesIntrospection(),
		},
	}
}

// menuIntrospection 描述 dbusmenu 的方法和 LayoutUpdated 信号。
func menuIntrospection() *introspect.Node {
	return &introspect.Node{
		Name: string(menuPath),
		Interfaces: []introspect.Interface{
			{
				Name: menuInterface,
				Methods: []introspect.Method{
					{
						Name: "GetLayout",
						Args: []introspect.Arg{
							{Name: "parentId", Type: "i", Direction: "in"},
							{Name: "recursionDepth", Type: "i", Direction: "in"},
							{Name: "propertyNames", Type: "as", Direction: "in"},
							{Name: "revision", Type: "u", Direction: "out"},
							{Name: "layout", Type: "(ia{sv}av)", Direction: "out"},
						},
					},
					{Name: "Event", Args: inArgs("i", "s", "v", "u")},
					{
						Name: "AboutToShow",
						Args: []introspect.Arg{
							{Name: "id", Type: "i", Direction: "in"},
							{Name: "needUpdate", Type: "b", Direction: "out"},
						},
					},
				},
				Signals: []introspect.Signal{
					{
						Name: "LayoutUpdated",
						Args: []introspect.Arg{
							{Name: "revision", Type: "u"},
							{Name: "parentId", Type: "i"},
						},
					},
				},
			},
		},
	}
}

// inArgs 构造只有输入参数的方法描述。
func inArgs(types ...string) []introspect.Arg {
	args := make([]introspect.Arg, len(types))
	for index, argType := range types {
		args[index] = introspect.Arg{Type: argType, Direction: "in"}
	}
	return args
}

// statusNotifierPropertyIntrospection 返回 SNI 属性的准确 D-Bus 签名。
func statusNotifierPropertyIntrospection() []introspect.Property {
	return []introspect.Property{
		{Name: "Category", Type: "s", Access: "read"},
		{Name: "Id", Type: "s", Access: "read"},
		{Name: "Title", Type: "s", Access: "read"},
		{Name: "Status", Type: "s", Access: "read"},
		{Name: "WindowId", Type: "u", Access: "read"},
		{Name: "IconThemePath", Type: "s", Access: "read"},
		{Name: "IconName", Type: "s", Access: "read"},
		{Name: "IconPixmap", Type: "a(iiay)", Access: "read"},
		{Name: "OverlayIconName", Type: "s", Access: "read"},
		{Name: "OverlayIconPixmap", Type: "a(iiay)", Access: "read"},
		{Name: "AttentionIconName", Type: "s", Access: "read"},
		{Name: "AttentionIconPixmap", Type: "a(iiay)", Access: "read"},
		{Name: "AttentionMovieName", Type: "s", Access: "read"},
		{Name: "ToolTip", Type: "(sa(iiay)ss)", Access: "read"},
		{Name: "ItemIsMenu", Type: "b", Access: "read"},
		{Name: "Menu", Type: "o", Access: "read"},
	}
}

// propertiesIntrospection 描述标准 D-Bus 属性接口。
func propertiesIntrospection() introspect.Interface {
	return introspect.Interface{
		Name: propertiesInterface,
		Methods: []introspect.Method{
			{
				Name: "Get",
				Args: []introspect.Arg{
					{Name: "interface", Type: "s", Direction: "in"},
					{Name: "property", Type: "s", Direction: "in"},
					{Name: "value", Type: "v", Direction: "out"},
				},
			},
			{
				Name: "GetAll",
				Args: []introspect.Arg{
					{Name: "interface", Type: "s", Direction: "in"},
					{Name: "properties", Type: "a{sv}", Direction: "out"},
				},
			},
			{Name: "Set", Args: inArgs("s", "s", "v")},
		},
		Signals: []introspect.Signal{
			{
				Name: "PropertiesChanged",
				Args: []introspect.Arg{
					{Name: "interface", Type: "s"},
					{Name: "changedProperties", Type: "a{sv}"},
					{Name: "invalidatedProperties", Type: "as"},
				},
			},
		},
	}
}
