//go:build !windows

package taskbar

import (
	"sync"
	"time"
)

// widget is a no-op stand-in on non-Windows platforms.
//
// The module still samples and publishes the metrics; only the taskbar-embedded
// window is unavailable, so the panel shows the numbers instead.
type widget struct {
	feat *Feature

	mu      sync.Mutex
	stats   Stats
	stopped bool
}

// newWidget builds the non-native widget.
func newWidget(f *Feature) *widget { return &widget{feat: f} }

// SetStats records the latest sample (nothing is rendered).
func (w *widget) SetStats(s Stats) {
	w.mu.Lock()
	w.stats = s
	w.mu.Unlock()
}

// Run returns immediately: there is no native message loop off Windows.
func (w *widget) Run() {}

// Stop is a no-op off Windows. It is idempotent.
func (w *widget) Stop() {
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
}

// Stats returns the last sample for callers that need it (tests, logging).
func (w *widget) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// humanUptime formats an uptime duration compactly.
func humanUptime(d time.Duration) string {
	if d <= 0 {
		return "up --"
	}
	total := int(d.Minutes())
	days, hours, mins := total/1440, (total/60)%24, total%60
	switch {
	case days > 0:
		return "up " + itoa(days) + "d" + itoa(hours) + "h"
	case hours > 0:
		return "up " + itoa(hours) + "h" + itoa(mins) + "m"
	default:
		return "up " + itoa(mins) + "m"
	}
}
