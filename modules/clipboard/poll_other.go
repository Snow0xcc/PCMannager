//go:build !windows

package clipboard

import "context"

// pollFiles 非 Windows 下没有 CF_HDROP：文件列表等价物（如 X11 的
// text/uri-list）语义不同，这里不做轮询，保持 no-op。
func (f *Feature) pollFiles(ctx context.Context) {}
