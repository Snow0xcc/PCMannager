package statusbar

import (
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

// Stats holds a single sampled snapshot of system metrics.
type Stats struct {
	CPU    float64 // percent 0-100
	RAM    float64 // percent 0-100
	NetUp  float64 // bytes/sec
	NetDn  float64 // bytes/sec
	Disk   float64 // percent 0-100 (optional)
}

// Collector samples system metrics on an interval.
type Collector struct {
	interval time.Duration
	lastUp   uint64
	lastDn   uint64
	ch       chan Stats
	done     chan struct{}
}

// NewCollector creates a collector that emits snapshots every interval.
func NewCollector(interval time.Duration) *Collector {
	if interval <= 0 {
		interval = time.Second
	}
	return &Collector{interval: interval, ch: make(chan Stats, 4), done: make(chan struct{})}
}

// Channel returns the stats output channel.
func (c *Collector) Channel() <-chan Stats { return c.ch }

// Run starts sampling until Stop is called.
func (c *Collector) Run() {
	prevUp, prevDn := c.counters()
	c.lastUp, c.lastDn = prevUp, prevDn
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			s := c.sample(prevUp, prevDn)
			prevUp, prevDn = s.rawUp, s.rawDn
			select {
			case c.ch <- s.Stats:
			default:
			}
		}
	}
}

// snapshot carries both displayed stats and raw counters.
type snapshot struct {
	Stats
	rawUp uint64
	rawDn uint64
}

func (c *Collector) counters() (uint64, uint64) {
	io, err := net.IOCounters(false)
	if err != nil || len(io) == 0 {
		return c.lastUp, c.lastDn
	}
	return io[0].BytesSent, io[0].BytesRecv
}

func (c *Collector) sample(prevUp, prevDn uint64) snapshot {
	s := snapshot{}
	if cpuPct, err := cpu.Percent(0, false); err == nil && len(cpuPct) > 0 {
		s.CPU = cpuPct[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		s.RAM = vm.UsedPercent
	}
	up, dn := c.counters()
	dt := c.interval.Seconds()
	if dt > 0 {
		if up >= prevUp {
			s.NetUp = float64(up-prevUp) / dt
		}
		if dn >= prevDn {
			s.NetDn = float64(dn-prevDn) / dt
		}
	}
	s.rawUp, s.rawDn = up, dn
	c.lastUp, c.lastDn = up, dn
	return s
}

// Stop halts sampling.
func (c *Collector) Stop() { close(c.done) }
