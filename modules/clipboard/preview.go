package clipboard

import "fmt"

// filePreview renders the detail pane for a file-list entry. It lives here
// (build-tag free) because the native viewer and the panel fallback render
// the exact same text on every platform.
func filePreview(e Entry) string {
	return fmt.Sprintf("[文件] %s\n%s\n\n选中“写回剪贴板”即可把该文件放回剪贴板。",
		singleLine(e.Text), e.Path)
}
