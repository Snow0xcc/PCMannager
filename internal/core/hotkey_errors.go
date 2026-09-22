package core

import "errors"

// errHotkeyUnsupported is returned when the platform has no native global
// hotkey support (non-Windows builds).
var errHotkeyUnsupported = errors.New("当前平台不支持全局热键（仅 Windows 提供）")
