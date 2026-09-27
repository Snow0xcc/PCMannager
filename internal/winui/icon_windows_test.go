//go:build windows

package winui

import (
	"image"
	"testing"
)

// TestIconFromRGBARoundTrip 验证实心图能造出真实 HICON，且释放句柄不会 panic。
func TestIconFromRGBARoundTrip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = 255 // R
		img.Pix[i+1] = 0
		img.Pix[i+2] = 0
		img.Pix[i+3] = 255
	}

	h := IconFromRGBA(img)
	if h == 0 {
		t.Fatal("IconFromRGBA 返回 0，未能创建图标")
	}
	DestroyIconHandle(h)
}

// TestIconFromRGBARejectsEmptyInput 覆盖非法输入：nil 与零尺寸图都应返回 0 而不是
// 崩溃（CreateDIBSection 对 0 尺寸的行为不可依赖，必须在进入前拦掉）。
func TestIconFromRGBARejectsEmptyInput(t *testing.T) {
	if h := IconFromRGBA(nil); h != 0 {
		DestroyIconHandle(h)
		t.Fatal("nil 图应返回 0")
	}
	if h := IconFromRGBA(image.NewRGBA(image.Rect(0, 0, 0, 0))); h != 0 {
		DestroyIconHandle(h)
		t.Fatal("零尺寸图应返回 0")
	}
}

// TestIconFromRGBASubImageStride 用带 stride 填充的子图覆盖逐行拷贝：把 32x32 图
// 裁出一块起点非零的子图，若按整块 copy 或忽略 Stride，取到的行会错位。
func TestIconFromRGBASubImageStride(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 48, 48))
	sub := img.SubImage(image.Rect(8, 8, 40, 40)).(*image.RGBA)

	h := IconFromRGBA(sub)
	if h == 0 {
		t.Fatal("子图未能创建图标")
	}
	DestroyIconHandle(h)
}

// TestDestroyIconHandleZero 覆盖空句柄：DestroyIcon(0) 本身无害，但我们仍提前
// 返回，这里确保该分支不 panic。
func TestDestroyIconHandleZero(t *testing.T) {
	DestroyIconHandle(0)
}
