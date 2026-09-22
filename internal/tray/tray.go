// Package tray renders the system-tray icon and its menu.
//
// The tray is implemented directly on Win32 (Shell_NotifyIcon) rather than
// through a third-party library: the popular Go tray packages need cgo on
// Linux, which would break GoBox's cgo-free build guarantee (PRD GFR-11).
package tray

// Item is a single menu entry exposed to the application.
type Item struct {
	// ID uniquely identifies the item within a menu rebuild.
	ID string
	// Title is the visible label.
	Title string
	// Checkable marks the item as a toggle; Checked carries its state.
	Checkable bool
	Checked   bool
	// Disabled greys the item out.
	Disabled bool
	// Separator renders a divider instead of a clickable entry.
	Separator bool
}

// Menu is an ordered list of tray menu items.
type Menu struct {
	// Tooltip is the hover text.
	Tooltip string
	Items   []Item
}

// Handler receives menu selections.
type Handler interface {
	// OnSelect is called with the ID of the clicked item.
	OnSelect(id string)
}

// HandlerFunc adapts a function to the Handler interface.
type HandlerFunc func(id string)

// OnSelect implements Handler.
func (f HandlerFunc) OnSelect(id string) { f(id) }

// Tray is the platform tray abstraction. The Windows implementation is backed
// by Shell_NotifyIcon; other platforms return a no-op implementation.
type Tray interface {
	// SetMenu replaces the tray menu.
	SetMenu(Menu)
	// Show makes the icon visible.
	Show() error
	// Hide removes the icon.
	Hide()
	// Visible reports whether the icon is shown.
	Visible() bool
	// Destroy releases the tray resources.
	Destroy()
}
