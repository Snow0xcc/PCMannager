package taskbar

import (
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	psnet "github.com/shirou/gopsutil/v3/net"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Stats is one sampled snapshot of the system metrics shown on the taskbar.
type Stats struct {
	CPU    float64 // percent 0-100
	RAM    float64 // percent 0-100
	NetUp  float64 // bytes/sec
	NetDn  float64 // bytes/sec
	Disk   float64 // percent 0-100
	Uptime time.Duration
}

// Collector samples system metrics on a fixed interval and publishes snapshots.
//
// It stops when the application context is cancelled or its own done channel is
// closed, so a module restart can never leave a sampler goroutine behind.
type Collector struct {
	mu       sync.Mutex
	interval time.Duration
	ctx      *core.Context
	lastUp   uint64
	lastDn   uint64
	ch       chan Stats
	done     chan struct{}
	stopOnce sync.Once
}

// NewCollector creates a collector that emits a snapshot every interval.
func NewCollector(ctx *core.Context, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = time.Second
	}
	return &Collector{
		interval: interval,
		ctx:      ctx,
		ch:       make(chan Stats, 4),
		done:     make(chan struct{}),
	}
}

// Channel returns the stats output channel.
func (c *Collector) Channel() <-chan Stats { return c.ch }

// SetInterval changes the sampling interval (applied on the next tick).
func (c *Collector) SetInterval(d time.Duration) {
	if d <= 0 {
		d = time.Second
	}
	c.mu.Lock()
	c.interval = d
	c.mu.Unlock()
}

// Interval returns the current sampling interval.
func (c *Collector) Interval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.interval
}

// Run samples until Stop is called or the application context is cancelled.
func (c *Collector) Run() {
	prevUp, prevDn := c.counters()

	// A resettable timer (rather than a Ticker) lets Option changes to the
	// sampling interval take effect without restarting the module.
	timer := time.NewTimer(c.Interval())
	defer timer.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-c.appCtxDone():
			return
		case <-timer.C:
			s := c.sample(prevUp, prevDn)
			prevUp, prevDn = s.rawUp, s.rawDn
			select {
			case c.ch <- s.Stats:
			default: // drop the sample rather than stall the sampler
			}
			timer.Reset(c.Interval())
		}
	}
}

// appCtxDone returns the application context's Done channel, or a nil channel
// when no context was supplied (a nil channel blocks forever, which is the
// desired behaviour for a standalone collector).
func (c *Collector) appCtxDone() <-chan struct{} {
	if c.ctx == nil || c.ctx.Ctx == nil {
		return nil
	}
	return c.ctx.Ctx.Done()
}

// snapshot carries both the displayed stats and the raw interface counters.
type snapshot struct {
	Stats
	rawUp uint64
	rawDn uint64
}

// counters reads the cumulative bytes sent/received on all interfaces.
func (c *Collector) counters() (uint64, uint64) {
	io, err := psnet.IOCounters(false)
	if err != nil || len(io) == 0 {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.lastUp, c.lastDn
	}
	return io[0].BytesSent, io[0].BytesRecv
}

// sample takes one measurement, deriving rates from the previous counters.
func (c *Collector) sample(prevUp, prevDn uint64) snapshot {
	s := snapshot{}
	if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
		s.CPU = pct[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		s.RAM = vm.UsedPercent
	}
	if du, err := diskUsage(); err == nil {
		s.Disk = du
	}
	if up, err := host.Uptime(); err == nil {
		s.Uptime = time.Duration(up) * time.Second
	}

	up, dn := c.counters()
	if dt := c.Interval().Seconds(); dt > 0 {
		if up >= prevUp {
			s.NetUp = float64(up-prevUp) / dt
		}
		if dn >= prevDn {
			s.NetDn = float64(dn-prevDn) / dt
		}
	}
	s.rawUp, s.rawDn = up, dn
	c.mu.Lock()
	c.lastUp, c.lastDn = up, dn
	c.mu.Unlock()
	return s
}

// Stop halts sampling. It is safe to call more than once.
func (c *Collector) Stop() {
	c.stopOnce.Do(func() { close(c.done) })
}

// diskUsage reports the used percentage of the platform's primary volume.
func diskUsage() (float64, error) {
	path := "/"
	switch {
	case isWindows():
		path = systemDrive()
	}
	du, err := disk.Usage(path)
	if err != nil {
		return 0, err
	}
	return du.UsedPercent, nil
}

// humanScale compacts a byte count into a number plus its binary prefix,
// without a trailing unit: 512, 19K, 1.2M, 3.4G.
//
// Callers append the unit they mean ("B/s" for bytes, "b/s" for bits), which
// is what makes the bytes/bits speed option possible without two formatters.
func humanScale(b float64) string {
	const (
		kb = 1024.0
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fG", b/gb)
	case b >= mb:
		return fmt.Sprintf("%.1fM", b/mb)
	case b >= kb:
		return fmt.Sprintf("%.0fK", b/kb)
	default:
		return fmt.Sprintf("%.0f", b)
	}
}
