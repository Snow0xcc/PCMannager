# PCMannager 交接文档

## 1. 项目状态

当前项目已从“旧 API 构建阻塞”阶段推进到“可编译、可继续迭代”的集成阶段。

已完成的关键工作：
- 统一所有模块到 `internal/core.Module` / `core.Context` 契约
- 去除了旧的 `core.App` / `core.Manager` / `Infof` / `FeatureCfg` 等阻塞点
- 完成 `internal/app`、`internal/server`、`internal/config` 的基础集成
- 补全 `modules/*` 的新式模块接口适配
- 通过了 `go vet`、`go test`、`gofmt` 与双平台构建验证

当前处于：
- 架构已稳定
- 关键功能已落地
- 运行时细节与 UI 收口仍需要进一步实测和打磨

阶段 A（解除运行时阻断）已在本轮完成代码修复与自动化测试，三个发布阻断项的当前状态：

| 项 | 状态 | 备注 |
| --- | --- | --- |
| P0-1 托盘同锁重入死锁 | 已修复 + 测试 | 锁协议改为"锁内快照、锁外通知"；**未做真实 Windows 实机验证** |
| P0-2 热键注册线程错误 | 已修复 + fake backend 测试 | 注册/注销移到泵线程执行；**未做真实 Windows 热键触发验证** |
| P0-3 Bus 发送/关闭竞态 | 已修复 + 压测 | `go test -race -count=5` 稳定通过 |

因此"架构已稳定"仅指模块契约与依赖方向稳定；**运行时行为仍未经真实 Windows 验收**，详见 `docs/PROJECT-AUDIT.md` 第 6.1 节。

---

## 2. 已验证的命令

以下命令均已实际执行并成功通过：

```bash
cd /workspaces/PCMannager

go vet ./...
go test ./...
gofmt -l .

go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```

验证结果摘要：
- `go vet ./...`：无问题
- `go test ./...`：全部通过
- `gofmt -l .`：无输出，说明格式已清理
- `go build ./...`：成功
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`：成功

---

## 3. 关键技术架构

### 核心契约
- [internal/core/module.go](../internal/core/module.go)
- 设计目标：统一模块能力，所有功能都实现 `core.Module`

### 应用编排
- [internal/app/app.go](../internal/app/app.go)
- [internal/app/provider.go](../internal/app/provider.go)
- 负责注册模块、热键、事件总线、托盘与生命周期管理

### HTTP / 首选项面板
- [internal/server/server.go](../internal/server/server.go)
- [internal/server/provider.go](../internal/server/provider.go)
- 提供 REST + SSE 接口，给前端或设置面板使用

### 配置管理
- [internal/config/config.go](../internal/config/config.go)
- 当前使用 YAML 配置，支持原子写入和默认值合并

### 模块目录
- [modules/taskbar](../modules/taskbar)
- [modules/clipboard](../modules/clipboard)
- [modules/screenshot](../modules/screenshot)
- [modules/selfcontext](../modules/selfcontext)
- [modules/repair](../modules/repair)
- [modules/preferences](../modules/preferences)

---

## 4. 当前实现重点

### 任务栏状态模块
- 位置： [modules/taskbar/feature.go](../modules/taskbar/feature.go)
- 目标：TrafficMonitor 风格的任务栏状态统计
- 特点：系统信息采集与窗口展现分离，具备跨平台降级能力

### 电脑修复模块
- 位置： [modules/repair/feature.go](../modules/repair/feature.go)
- 位置： [modules/repair/catalog.go](../modules/repair/catalog.go)
- 目标：声明式修复/工具安装入口，按类别组织修复动作

### 剪贴板模块
- 位置： [modules/clipboard/feature.go](../modules/clipboard/feature.go)
- 目标：Ditto 风格的剪贴板历史管理
- 设计：支持历史记录、回写、重复去重与跨平台降级

### 截图模块
- 位置： [modules/screenshot/feature.go](../modules/screenshot/feature.go)
- 目标：Snipaste 风格的截图与编辑流程

### 上下文记录模块
- 位置： [modules/selfcontext/feature.go](../modules/selfcontext/feature.go)
- 目标：记录当前窗口上下文/工作状态，支持回看与摘要

### 首选项面板
- 位置： [modules/preferences/manager.go](../modules/preferences/manager.go)
- 目标：统一配置入口、热键管理和模块开关控制

---

## 5. 现状中的风险 / 待收口事项

以下事项仍建议接手人继续检查：

1. 运行时 UI 细节
   - 托盘图标/菜单展示
   - 任务栏嵌入窗口的尺寸与定位
   - 截图编辑器的交互细节

2. 真实 Windows 环境测试
   - 需要在 Windows 环境下实际执行热键、截图、复制、任务栏窗口等功能

3. 交互与日志一致性
   - 面板展示与真实模块状态需要再做一轮对齐校验

4. 细节稳定性
   - 升级/降级分支、异常恢复、后台 goroutine 生命周期

---

## 6. 接手建议

建议下一位接手者优先顺序：

1. 在 Windows 环境运行主程序
2. 打开首选项面板和各模块配置
3. 验证热键是否绑定正常
4. 验证截图、剪贴板、任务栏和修复动作
5. 检查日志输出与状态同步是否稳定

---

## 7. 结语

项目当前已经完成了最关键的架构重构和功能落地，具备继续迭代的基础。后续最重要的是以真实 Windows 运行环境做最后一轮功能级验证和体验收口，而不是再返回底层重构。

如果后续需要继续推进，下一步最优先是：
- Windows 运行时 smoke test
- 模块行为一致性校验
- UI/交互与异常处理收口
