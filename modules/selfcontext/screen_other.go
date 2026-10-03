//go:build !windows

package selfcontext

import (
	"context"
	"errors"
)

// CaptureScreen / DescribeScreen 仅 Windows 实现：截屏依赖 GDI，
// 非 Windows 侧保持"只记窗口标题"的既有能力。
var errScreenUnsupported = errors.New("selfcontext: 屏幕捕获仅 Windows 支持")

func CaptureScreen() ([]byte, error) { return nil, errScreenUnsupported }

func DescribeScreen(context.Context, VLMConfig, []byte) (string, error) {
	return "", errScreenUnsupported
}

// VLMConfig 非 Windows 下同样不可用，保留类型以便配置代码跨平台编译。
type VLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}
