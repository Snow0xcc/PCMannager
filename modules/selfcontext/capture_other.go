//go:build !windows

package selfcontext

import "fmt"

// ActiveWindow is a no-op outside Windows: there is no portable way to read the
// focused window without cgo, so the recorder simply has nothing to sample.
func ActiveWindow() (title, process string, err error) { return "", "", nil }

// sessionLocked always reports false outside Windows, where the recorder has no
// session to lock.
func sessionLocked() bool { return false }

// copyToClipboard is unavailable outside Windows: no clipboard API is reachable
// without cgo, so the summary stays available through the panel instead.
func copyToClipboard(text string) error {
	return fmt.Errorf("selfcontext: 复制摘要仅 Windows 支持")
}
