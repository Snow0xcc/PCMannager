//go:build !windows

package launcher

import (
	"fmt"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// panelState 非 Windows 降级：面板是原生窗口能力，其它平台经由面板动作提示
// 不可用（与截图编辑器的降级策略一致）。
type panelState struct{}

func newPanel(ctx *core.Context, f *Feature) *panelState { return &panelState{} }

func (p *panelState) isVisible() bool                    { return false }
func (p *panelState) present([]command, int, *rankStore) {}
func (p *panelState) hide()                              {}
func (p *panelState) close()                             {}

// openURL 非 Windows 走 sysutil（内部已处理 xdg-open），行为一致，无需包装。
func openURL(url string) error { return fmt.Errorf("launcher: 网页快捷方式仅 Windows 支持") }
