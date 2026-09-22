//go:build !windows

package selfcontext

// ActiveTitle is a no-op outside Windows.
func ActiveTitle() (string, error) { return "", nil }
