//go:build windows

package screenshot

import (
	"image"
	"image/draw"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// editorState tracks the in-progress rectangle selection.
type editorState struct {
	img      *image.RGBA
	bounds   image.Rectangle
	full     *walk.Bitmap
	sel      *walk.Rectangle
	dragging bool
	onSave   func(image.Image)
	onCopy   func(image.Image)
	onCancel func()
}

var state *editorState

// openEditor shows a full-screen window with the captured image and lets the
// user drag to select a region, then Save / Copy / Cancel.
func openEditor(app interface{}, img *image.RGBA, bounds image.Rectangle, onSave, onCopy func(image.Image)) {
	bmp, err := walk.NewBitmapFromImage(img)
	if err != nil {
		return
	}
	state = &editorState{
		img:    img,
		bounds: bounds,
		full:   bmp,
		sel:    &walk.Rectangle{},
		onSave: onSave,
		onCopy: onCopy,
	}

	var mw *walk.MainWindow
	var cw *walk.CustomWidget

	paint := func(canvas *walk.Canvas, _ walk.Rectangle) error {
		if state.full != nil {
			_ = canvas.DrawImage(state.full, walk.Point{X: 0, Y: 0})
		}
		if state.sel != nil && (state.sel.Width > 2 || state.sel.Height > 2) {
			pen, err := walk.NewCosmeticPen(walk.PenSolid, walk.RGB(80, 180, 255))
			if err == nil {
				defer pen.Dispose()
				_ = canvas.DrawRectangle(pen, *state.sel)
			}
		}
		return nil
	}

	mwMouseDown := func(x, y int, _ walk.MouseButton) {
		state.dragging = true
		state.sel.X = x
		state.sel.Y = y
		state.sel.Width = 0
		state.sel.Height = 0
	}
	mwMouseMove := func(x, y int, _ walk.MouseButton) {
		if !state.dragging {
			return
		}
		state.sel.Width = x - state.sel.X
		state.sel.Height = y - state.sel.Y
		if cw != nil {
			cw.Invalidate()
		}
	}
	mwMouseUp := func(x, y int, _ walk.MouseButton) {
		if !state.dragging {
			return
		}
		state.dragging = false
		state.sel.Width = x - state.sel.X
		state.sel.Height = y - state.sel.Y
	}

	doSave := func() {
		if r := cropRect(); r != nil {
			state.onSave(cropImage(r))
		}
		mw.Close()
	}
	doCopy := func() {
		if r := cropRect(); r != nil {
			state.onCopy(cropImage(r))
		}
		mw.Close()
	}

	MainWindow{
		AssignTo: &mw,
		Title:    "PCMannager - 截图 (Snipaste)",
		MinSize:  Size{Width: state.img.Bounds().Dx(), Height: state.img.Bounds().Dy()},
		Size:     Size{Width: state.img.Bounds().Dx(), Height: state.img.Bounds().Dy()},
		Layout:   VBox{},
		Children: []Widget{
			CustomWidget{
				AssignTo: &cw,
				StretchFactor: 1,
				MinSize:       Size{Width: state.img.Bounds().Dx(), Height: state.img.Bounds().Dy()},
				Paint:         paint,
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "保存 (Enter)", OnClicked: doSave},
					PushButton{Text: "复制到剪贴板 (C)", OnClicked: doCopy},
					PushButton{Text: "取消 (Esc)", OnClicked: func() { mw.Close() }},
				},
			},
		},
	}.Create()
	// wire mouse + keyboard after the window exists
	mw.MouseDown().Attach(mwMouseDown)
	mw.MouseMove().Attach(mwMouseMove)
	mw.MouseUp().Attach(mwMouseUp)
	mw.KeyDown().Attach(func(key walk.Key) {
		switch key {
		case walk.KeyReturn:
			doSave()
		case walk.KeyEscape:
			mw.Close()
		case walk.KeyC:
			doCopy()
		}
	})
	mw.Run()
}

// cropRect converts the walk.Rectangle selection into an image.Rectangle,
// normalising negative width/height.
func cropRect() *image.Rectangle {
	if state.sel == nil {
		return nil
	}
	r := image.Rect(state.sel.X, state.sel.Y, state.sel.X+state.sel.Width, state.sel.Y+state.sel.Height)
	if r.Dx() < 2 || r.Dy() < 2 {
		return nil
	}
	// normalise
	if r.Min.X > r.Max.X {
		r.Min.X, r.Max.X = r.Max.X, r.Min.X
	}
	if r.Min.Y > r.Max.Y {
		r.Min.Y, r.Max.Y = r.Max.Y, r.Min.Y
	}
	b := state.img.Bounds()
	if r.Min.X < b.Min.X {
		r.Min.X = b.Min.X
	}
	if r.Min.Y < b.Min.Y {
		r.Min.Y = b.Min.Y
	}
	if r.Max.X > b.Max.X {
		r.Max.X = b.Max.X
	}
	if r.Max.Y > b.Max.Y {
		r.Max.Y = b.Max.Y
	}
	return &r
}

// cropImage returns a copy of the selected region.
func cropImage(r *image.Rectangle) *image.RGBA {
	out := image.NewRGBA(r.Bounds())
	draw.Draw(out, r.Bounds(), state.img, r.Min, draw.Src)
	return out
}
