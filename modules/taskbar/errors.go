package taskbar

import "errors"

// Module-level errors.
var (
	// errFeatureNil is returned when a method runs before Init.
	errFeatureNil = errors.New("taskbar: 模块未初始化")
	// errInterval rejects a non-positive sampling interval.
	errInterval = errors.New("taskbar: interval 需要正整数（毫秒）")
	// errNoTaskbar is reported when Shell_TrayWnd cannot be found, so the
	// widget falls back to a floating top-level window instead of failing.
	errNoTaskbar = errors.New("taskbar: 未找到任务栏窗口")
)

// unknownOptionError is returned for options the module does not declare.
type unknownOptionError struct{ key string }

func (e *unknownOptionError) Error() string { return "未知配置项: " + e.key }

// unknownActionError is returned for action ids the module does not declare.
type unknownActionError struct{ id string }

func (e *unknownActionError) Error() string { return "未知操作: " + e.id }
