//go:build !windows

package preferences

import "fmt"

// formatValue renders a config value for the text fallback widgets.
func formatValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
