//go:build linux

package tray

import (
	"image"
	"image/color"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/snow0xcc/pcmannager/internal/logo"
)

// TestBuildMenuLayout 守护 Item 到 dbusmenu 节点的映射：根节点固定为 0，
// 子节点编号连续，分隔符、勾选态和禁用态都使用协议规定的属性。
func TestBuildMenuLayout(t *testing.T) {
	items := []Item{
		{ID: "open", Title: "打开", Color: "orange"},
		{ID: "separator", Separator: true},
		{ID: "enabled", Title: "已启用", Checkable: true, Checked: true},
		{ID: "disabled", Title: "未启用", Checkable: true, Disabled: true},
	}
	layout := buildMenuLayout(items)

	if layout.ID != 0 {
		t.Fatalf("根节点 ID = %d，期望 0", layout.ID)
	}
	if len(layout.Children) != len(items) {
		t.Fatalf("子节点数 = %d，期望 %d", len(layout.Children), len(items))
	}
	if got := dbus.SignatureOf(layout).String(); got != "(ia{sv}av)" {
		t.Fatalf("菜单布局签名 = %q，期望 %q", got, "(ia{sv}av)")
	}

	cases := []struct {
		name       string
		properties map[string]any
	}{
		{
			name: "普通项忽略颜色",
			properties: map[string]any{
				"label":   "打开",
				"enabled": true,
			},
		},
		{
			name: "分隔符",
			properties: map[string]any{
				"type": "separator",
			},
		},
		{
			name: "已勾选项",
			properties: map[string]any{
				"label":        "已启用",
				"enabled":      true,
				"toggle-type":  "checkmark",
				"toggle-state": int32(1),
			},
		},
		{
			name: "未勾选且禁用项",
			properties: map[string]any{
				"label":        "未启用",
				"enabled":      false,
				"toggle-type":  "checkmark",
				"toggle-state": int32(0),
			},
		},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			child := layout.Children[index]
			node, ok := child.Value().(dbusMenuLayout)
			if !ok {
				t.Fatalf("子节点类型 = %T，期望 dbusMenuLayout", child.Value())
			}
			if node.ID != int32(index+1) {
				t.Errorf("子节点 ID = %d，期望 %d", node.ID, index+1)
			}
			if len(node.Properties) != len(test.properties) {
				t.Fatalf("属性数 = %d，期望 %d；实际属性为 %v",
					len(node.Properties), len(test.properties), node.Properties)
			}
			for name, want := range test.properties {
				value, ok := node.Properties[name]
				if !ok {
					t.Errorf("缺少属性 %q", name)
					continue
				}
				if got := value.Value(); !reflect.DeepEqual(got, want) {
					t.Errorf("属性 %q = %#v，期望 %#v", name, got, want)
				}
			}
		})
	}
}

// TestRGBAtoIconPixmap 守护图标尺寸、长度与逐像素 A、R、G、B 顺序，
// 同时用品牌图标的小尺寸输出覆盖真实渲染结果。
func TestRGBAtoIconPixmap(t *testing.T) {
	known := image.NewRGBA(image.Rect(0, 0, 1, 1))
	known.SetRGBA(0, 0, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44})
	knownPixmap := rgbaToIconPixmap(known)
	if len(knownPixmap) != 1 {
		t.Fatalf("单尺寸图标层数 = %d，期望 1", len(knownPixmap))
	}
	wantPixel := []byte{0x44, 0x11, 0x22, 0x33}
	if got := knownPixmap[0].Data; len(got) != len(wantPixel) ||
		got[0] != wantPixel[0] || got[1] != wantPixel[1] ||
		got[2] != wantPixel[2] || got[3] != wantPixel[3] {
		t.Fatalf("ARGB 像素 = %v，期望 %v", got, wantPixel)
	}

	img := logo.Render(4)
	pixmaps := rgbaToIconPixmap(img)
	if len(pixmaps) != 1 {
		t.Fatalf("品牌图标层数 = %d，期望 1", len(pixmaps))
	}
	pixmap := pixmaps[0]
	if pixmap.Width != 4 || pixmap.Height != 4 {
		t.Fatalf("品牌图标尺寸 = %dx%d，期望 4x4", pixmap.Width, pixmap.Height)
	}
	if len(pixmap.Data) != 4*4*4 {
		t.Fatalf("品牌图标数据长度 = %d，期望 %d", len(pixmap.Data), 4*4*4)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			source := img.PixOffset(x, y)
			target := (y*4 + x) * 4
			want := []byte{
				img.Pix[source+3],
				img.Pix[source],
				img.Pix[source+1],
				img.Pix[source+2],
			}
			got := pixmap.Data[target : target+4]
			for channel := range want {
				if got[channel] != want[channel] {
					t.Fatalf("像素 (%d,%d) 的 ARGB = %v，期望 %v", x, y, got, want)
				}
			}
		}
	}
	if got := dbus.SignatureOf(pixmaps).String(); got != "a(iiay)" {
		t.Fatalf("IconPixmap 签名 = %q，期望 %q", got, "a(iiay)")
	}
}

// TestDBusMenuClickedEvent 守护数字菜单 ID 到稳定 Item.ID 的转换，并确认
// 非 clicked 事件及不存在的节点不会触发业务回调。
func TestDBusMenuClickedEvent(t *testing.T) {
	var selected []string
	tray := &linuxTray{
		handler: HandlerFunc(func(id string) {
			selected = append(selected, id)
		}),
		menu: Menu{Items: []Item{
			{ID: "first", Title: "第一项"},
			{ID: "second", Title: "第二项"},
		}},
	}
	menu := &dbusMenu{tray: tray}

	cases := []struct {
		name      string
		id        int32
		eventID   string
		wantCalls int
		wantLast  string
	}{
		{name: "第二项点击", id: 2, eventID: "clicked", wantCalls: 1, wantLast: "second"},
		{name: "非点击事件", id: 1, eventID: "hovered", wantCalls: 1, wantLast: "second"},
		{name: "未知节点", id: 9, eventID: "clicked", wantCalls: 1, wantLast: "second"},
		{name: "第一项点击", id: 1, eventID: "clicked", wantCalls: 2, wantLast: "first"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := menu.Event(test.id, test.eventID, dbus.MakeVariant(""), 0); err != nil {
				t.Fatalf("Event 返回错误: %v", err)
			}
			if len(selected) != test.wantCalls {
				t.Fatalf("回调次数 = %d，期望 %d", len(selected), test.wantCalls)
			}
			if len(selected) != 0 && selected[len(selected)-1] != test.wantLast {
				t.Fatalf("最后回调 ID = %q，期望 %q", selected[len(selected)-1], test.wantLast)
			}
		})
	}
}
