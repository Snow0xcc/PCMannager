package pcrepair

import (
	"github.com/snow0xcc/pcmannager/internal/core"
)

// Feature implements a Windows PC repair and tool-installation panel.
type Feature struct {
	app *core.App
}

// NewFeature constructs the pcrepair feature.
func NewFeature() *Feature {
	return &Feature{}
}

func (f *Feature) Name() string  { return "pcrepair" }
func (f *Feature) Title() string { return "电脑修复与工具安装" }

func (f *Feature) Init(app *core.App) error {
	f.app = app
	return nil
}

func (f *Feature) Start() error {
	if !f.app.FeatureCfg("pcrepair").Enabled {
		return nil
	}
	f.app.Log.Infof("pcrepair feature started")
	return nil
}

func (f *Feature) Stop() error {
	return nil
}

// OnHotkey opens the repair/tool panel.
func (f *Feature) OnHotkey() error {
	return f.OpenUI()
}

// OpenUI shows the repair/tool panel in a goroutine.
func (f *Feature) OpenUI() error {
	go openPanel(f)
	return nil
}
