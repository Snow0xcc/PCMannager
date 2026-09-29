//go:build !windows

package launcher

import (
	"fmt"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// superPanelState 非 Windows 降级：超级面板是原生窗口能力，其它平台经动作
// 提示不可用（与搜索面板的降级策略一致）。
type superPanelState struct{}

func newSuperPanel(ctx *core.Context, f *Feature) *superPanelState { return &superPanelState{} }

func (p *superPanelState) isVisible() bool  { return false }
func (p *superPanelState) present([]string) {}
func (p *superPanelState) hide()            {}
func (p *superPanelState) close()           {}

// ToggleSuper 非 Windows 提示不可用（超级面板是原生窗口能力）。
func (f *Feature) ToggleSuper() error {
	return fmt.Errorf("launcher: 超级面板仅 Windows 支持")
}
