---
name: test-writer
description: 为 PCMannager 的 Go 代码按项目既有约定编写单元测试。新增功能或修复缺陷后使用。
---

为 `$ARGUMENTS` 编写测试，遵循项目约定：

1. 表驱动风格，与同包既有 `*_test.go` 一致；测试文件与被测包同目录。
2. 避开 AGENTS.md 记录的三个已知坑：
   - `App.Enable` 的幂等守卫是 `enabled && running`，必须经 `EnableModule`（先落盘开关）才能触发；直接调 `Enable` 不生效。
   - 非 Windows 下 `HotkeyManager.Conflicts()` 恒为空（后端恒返回 `errHotkeyUnsupported`）；冲突检测由 `internal/core` 的 fake backend 覆盖，其它包不要重复断言。
   - `Manager.ModuleIDs()` = 注册顺序 + 配置独有模块（默认配置恒带 5 个模块），应断言前缀顺序，不要断言"等于注册的 N 个"。
3. 并发相关代码用 `go test -race -count=1 ./internal/... ./modules/...` 验证。
4. Windows 专属逻辑只测平台无关部分（采集/格式化/解析），Win32 调用路径不做单测。
5. 运行新增测试并如实报告结果；失败不得谎报通过。
