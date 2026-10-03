package screenshot

import (
	"image"
	"sync"
)

// edMode selects which workflow the region editor drives.
//
// The same window, mask and selection code serves all three: only the toolbar
// and the action bound to the selection differ. Keeping one editor avoids three
// near-identical overlay implementations drifting apart.
type edMode int

const (
	// edModeCapture crops the selection, annotates it and saves/copies it.
	edModeCapture edMode = iota
	// edModeRecord turns the selection into a GIF recording region.
	edModeRecord
	// edModeScroll turns the selection into a scrolling long-shot region.
	edModeScroll
)

// editorHooks groups the editor's callbacks so openEditor's signature stays
// stable as modes are added.
//
// The editor deliberately knows nothing about the Feature: it only reports a
// chosen region and asks for a capture to start or stop, which keeps the window
// code independent of the recording and stitching machinery behind these hooks.
type editorHooks struct {
	// Save and Copy are used in capture mode.
	Save func(image.Image)
	Copy func(image.Image)
	// StartRecord begins a GIF recording of the chosen region.
	StartRecord func(image.Rectangle) error
	// StartScroll begins a scrolling capture; auto selects injected scrolling.
	StartScroll func(region image.Rectangle, auto bool) error
	// Stop ends the in-flight capture, keeping the result, and returns a short
	// description of what was produced for the user.
	Stop func() string
	// Abort discards the in-flight capture without keeping anything.
	Abort func()
	// Status reports live progress text and whether the capture has ended on its
	// own (frame cap reached, or stitching finished), so the editor can collect
	// the result without the user having to press anything.
	Status func() (text string, done bool)
	// CopyResult re-copies an already saved capture file to the clipboard.
	CopyResult func(path string) error
	// Reveal opens the folder holding a saved capture file.
	Reveal func(dir string)
}

// captureOutcome carries a capture's result from the module back to the editor
// that started it.
//
// A single shared slot is enough: captures are serialised by the module's busy
// flag, so there is never more than one editor and one capture at a time.
type captureOutcome struct {
	mu   sync.Mutex
	ext  string
	path string
}

// set records the result text and (optional) saved path.
func (o *captureOutcome) set(text, path string) {
	o.mu.Lock()
	o.ext, o.path = text, path
	o.mu.Unlock()
}

// take returns the recorded result and clears it.
func (o *captureOutcome) take() (string, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	text, path := o.ext, o.path
	o.ext, o.path = "", ""
	return text, path
}

// sharedOutcome is the one slot a capture's result travels through.
var sharedOutcome captureOutcome
