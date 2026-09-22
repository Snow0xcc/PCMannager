# 模块契约（Module Contract）

凡是位于 `modules/<id>/` 的包，都必须实现 `internal/core.Module`。

> **重要**：`core.App`、`core.Manager`、`core.Feature`、`core.NewLogger`、
> `config.FeatureConfig` **均已删除**，引用它们会直接导致编译失败。

## 接口定义

```go
type Module interface {
	ID() string
	Name() string
	Description() string
	Options() []Option
	Actions() []Action
	Init(ctx *Context) error
	Start() error
	Stop() error
	State() State
	OnHotkey() error
	OpenUI() error
	ApplyOption(key string, value any) error
}
```

嵌入 `core.Base` 可获得 `Options/Actions/State/OnHotkey/OpenUI/ApplyOption` 的默认
空实现，只需覆写用得到的部分。

## 标准骨架

```go
package taskbar

import (
	"github.com/snow0xcc/pcmannager/internal/core"
)

const moduleID = "taskbar"

// Feature 实现任务栏状态统计模块。
type Feature struct {
	core.Base

	ctx   *core.Context
	stop  chan struct{}
	// ...其余字段
}

// NewFeature 构造模块，返回值统一为 core.Module。
func NewFeature() core.Module { return &Feature{} }

func (f *Feature) ID() string          { return moduleID }
func (f *Feature) Name() string        { return "任务栏状态统计" }
func (f *Feature) Description() string { return "在任务栏显示 CPU/内存/网络/磁盘" }

func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: "interval", Label: "刷新间隔(ms)", Kind: core.KindInt,
			Default: 1000, Min: 200, Max: 10000, Step: 100},
	}
}

func (f *Feature) Init(ctx *core.Context) error {
	f.ctx = ctx
	return nil
}

func (f *Feature) Start() error {
	f.stop = make(chan struct{})
	// ...
	return nil
}

func (f *Feature) Stop() error {
	if f.stop != nil {
		close(f.stop)
		f.stop = nil // 幂等：重复 Stop 不得 panic
	}
	return nil
}
```

## 硬性规则

1. **禁止 cgo 依赖**：不得 import `github.com/lxn/walk`、`github.com/gonutz/w32`、
   `github.com/getlantern/systray`、`github.com/robotn/gohook`。
   它们会导致 `CGO_ENABLED=0 GOOS=windows` 交叉编译失败，违反 GFR-11。
2. **Win32 一律走 `internal/winui`**。平台相关文件必须成对：
   `_windows.go`（`//go:build windows`）与 `_other.go`（`//go:build !windows`），
   且两端导出**同名同签名**函数。
3. **import `internal/winui` 的文件必须带 `//go:build windows`**：
   `internal/winui/winui_other.go` 只 stub 了少量函数，其余在非 Windows 下不存在。
4. **包名 = 目录名**：`modules/taskbar` 的包名必须是 `taskbar`（原为 `statusbar`），
   `modules/repair` 必须是 `repair`（原为 `pcrepair`）。
5. **构造函数签名统一**：`func NewFeature() core.Module`。
6. **日志用 slog**：`f.ctx.Logger.Info("msg", "k", v)`，
   **不要**用 logrus 风格的 `Infof/Errorf/Warnf`（已随旧 Logger 删除）。
7. **配置读写**：`f.ctx.Config.Get(key, default)` / `f.ctx.Config.Set(key, value)`；
   开关与热键用 `f.ctx.Config.Enabled()` / `f.ctx.Config.Hotkey()`。
8. **事件上报**：`f.ctx.Bus.Log(id, "info", msg)`、`Bus.Progress(id, action, pct, line)`、
   `Bus.Notice(id, msg)`、`Bus.State(id, data)`。
9. **生命周期**：`Stop()` 必须幂等；所有后台 goroutine 必须响应 `f.ctx.Ctx.Done()`，
   不得泄漏；`Start()` 失败要返回 error 且不留半初始化状态。
10. **`Options()` 的 `Default` 必须与 `internal/config/config.go` 中 `Default()`
    里该模块的默认值一致**，否则面板显示与实际行为不符。
11. **模块 id 必须与目录名一致**（`taskbar`/`clipboard`/`screenshot`/`selfcontext`/
    `repair`），因为 `Registry`、配置、热键绑定都按 id 查找。
12. **模块 panic 会被 app 层 recover**，但不要依赖这一点；错误要显式返回。

## 验证命令

每个模块改完后必须全部通过：

```bash
gofmt -l modules/ internal/                                  # 必须无输出
go vet ./...                                                 # Linux
go build ./...                                               # Linux
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...       # Windows（发布目标）
```

## 常用 core 类型速查

| 类型 | 用途 |
|---|---|
| `core.Option` | 声明式配置项（`KindBool/Int/String/Select/Color`） |
| `core.Action` | 面板按钮（`KindNormal/Danger/Install/Open`，`Confirm` 二次确认，`Admin` 需提权，`Params` 占位符） |
| `core.State` | `map[string]any`，模块运行态快照，SSE 推给面板 |
| `core.Context` | `Ctx/Logger/Bus/Config/App/DataDir` |
| `core.AppControl` | `Shutdown()/PanelURL()/OpenPanel()/EnableModule(id,on)` |
