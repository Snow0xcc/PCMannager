package screenshot

import "github.com/snow0xcc/pcmannager/internal/winui"

// copyFileToClipboard offers a saved file to the clipboard as a file drop.
//
// A recorded GIF cannot be put on the Windows clipboard as CF_BITMAP — that
// format carries a raw device-independent bitmap, not an animation — so the file
// itself is offered as CF_HDROP instead. That is also what a chat client pastes
// as an attachment, which is the practical way to send a recording.
//
// winui provides the platform pairing, so this needs no build tags of its own.
func copyFileToClipboard(path string) error {
	return winui.ClipboardFileDrop([]string{path})
}
