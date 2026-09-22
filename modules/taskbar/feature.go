package statusbar

import (
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Feature implements the TrafficMonitor-style taskbar status widget.
type Feature struct {
	app       *core.App
	collector *Collector
	win       *window
	cfg       *statusbarCfg
	stopCh    chan struct{}
}

// statusbarCfg is a minimal view of the config relevant to this feature.
type statusbarCfg struct {
	Enabled  bool
	ShowCPU  bool
	ShowRAM  bool
	ShowNet  bool
	ShowDisk bool
	UpdateMs int
}

// NewFeature constructs the statusbar feature.
func NewFeature() *Feature { return &Feature{} }

func (f *Feature) Name() string  { return "statusbar" }
func (f *Feature) Title() string { return "任务栏状态统计 (TrafficMonitor)" }

func (f *Feature) Init(app *core.App) error {
	f.app = app
	c := &app.Config.Statusbar
	f.cfg = &statusbarCfg{c.Enabled, c.ShowCPU, c.ShowRAM, c.ShowNet, c.ShowDisk, c.UpdateMs}
	f.collector = NewCollector(intervalOf(f.cfg.UpdateMs))
	return nil
}

func intervalOf(ms int) time.Duration {
	if ms <= 0 {
		ms = 1000
	}
	return time.Duration(ms) * time.Millisecond
}

func (f *Feature) Start() error {
	if !f.cfg.Enabled {
		return nil
	}
	f.stopCh = make(chan struct{})
	go f.collector.Run()
	go f.pump()
	// TrafficMonitor-style: show the taskbar widget immediately when enabled.
	f.spawnWindow()
	f.app.Log.Infof("statusbar started (cpu=%v ram=%v net=%v)", f.cfg.ShowCPU, f.cfg.ShowRAM, f.cfg.ShowNet)
	return nil
}

// pump forwards sampled stats to the window (Windows) or a fallback logger.
func (f *Feature) pump() {
	for {
		select {
		case <-f.stopCh:
			return
		case s := <-f.collector.Channel():
			if f.win != nil {
				f.win.SetStats(s)
			} else {
				f.app.Log.Infof("stat cpu=%.0f%% mem=%.0f%% up=%s dn=%s",
					s.CPU, s.RAM, humanRate(s.NetUp), humanRate(s.NetDn))
			}
		}
	}
}

func (f *Feature) Stop() error {
	if f.collector != nil {
		f.collector.Stop()
	}
	if f.win != nil {
		f.win.Stop()
	}
	if f.stopCh != nil {
		close(f.stopCh)
	}
	return nil
}

// OnHotkey toggles the on-screen widget visibility.
func (f *Feature) OnHotkey() error {
	if f.win != nil {
		f.win.Stop()
		f.win = nil
	} else {
		f.spawnWindow()
	}
	return nil
}

// OpenUI logs current stats (platform window has no separate dialog).
func (f *Feature) OpenUI() error {
	if f.win == nil {
		f.spawnWindow()
	}
	return nil
}

// spawnWindow creates the platform taskbar window on a goroutine.
func (f *Feature) spawnWindow() {
	f.win = newWindow(f.cfg)
	go f.win.Run()
}
