package statusbar

import "fmt"

const (
	kb = 1024.0
	mb = kb * 1024
	gb = mb * 1024
)

// humanRate formats a bytes/sec value as a compact rate string.
func humanRate(bps float64) string {
	switch {
	case bps >= gb:
		return fmt.Sprintf("%.1fGB/s", bps/gb)
	case bps >= mb:
		return fmt.Sprintf("%.1fMB/s", bps/mb)
	case bps >= kb:
		return fmt.Sprintf("%.0fKB/s", bps/kb)
	default:
		return fmt.Sprintf("%.0fB/s", bps)
	}
}

// humanBytes formats a plain byte count.
func humanBytes(b float64) string {
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fGB", b/gb)
	case b >= mb:
		return fmt.Sprintf("%.1fMB", b/mb)
	case b >= kb:
		return fmt.Sprintf("%.0fKB", b/kb)
	default:
		return fmt.Sprintf("%.0fB", b)
	}
}
