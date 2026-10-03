//go:build windows

package selfcontext

import "encoding/base64"

// base64Std 是 DescribeScreen 依赖的编码入口（单独成函数便于测试与替换）。
func base64Std(p []byte) string { return base64.StdEncoding.EncodeToString(p) }
