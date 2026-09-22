package taskbar

import "errors"

// Module-level errors.
var (
	// errFeatureNil is returned when a method runs before Init.
	errFeatureNil = errors.New("taskbar: 模块未初始化")
	// errInterval rejects a non-positive sampling interval.
	errInterval = errors.New("taskbar: interval 需要正整数（毫秒）")
)

// unknownOptionError is returned for options the module does not declare.
type unknownOptionError struct{ key string }

func (e *unknownOptionError) Error() string { return "未知配置项: " + e.key }

// unknownActionError is returned for action ids the module does not declare.
type unknownActionError struct{ id string }

func (e *unknownActionError) Error() string { return "未知操作: " + e.id }
