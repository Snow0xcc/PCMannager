package tray

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// embeddedIconICO 是提交入库的品牌图标资产（五档 PNG-in-ICO）。重新生成：
//
//	go run scripts/genicon.go
//
// 资产随仓库分发，普通构建不跑生成器；只有 internal/logo 变更才需要重生成。
//
//go:embed assets/icon.ico
var embeddedIconICO []byte

// icoEntry 是 ICO 容器的一帧：方形位图（本项目恒为 PNG 载荷）加声明尺寸。
type icoEntry struct {
	width, height int
	payload       []byte
}

// parseICO 校验 ICO 容器并按文件顺序返回各帧。任何结构性畸形（截断头、
// 类型非图标、零帧、条目指向缓冲区外）都必须报错而不是 panic——资产是
// 编译期内嵌的，损坏时应让测试失败，而不是在托盘启动时崩溃。
//
// ICO 布局：ICONDIR（6 字节：reserved u16、type u16=1、count u16）
// 后跟 count 条 ICONDIRENTRY（各 16 字节），载荷位于 dwImageOffset 处。
// 宽高字段为 u8，0 表示 256。
func parseICO(data []byte) ([]icoEntry, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("icon: 数据 %d 字节，不足 6 字节 ICONDIR", len(data))
	}
	if binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, fmt.Errorf("icon: type = %d，期望 1（图标）", binary.LittleEndian.Uint16(data[2:4]))
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 {
		return nil, fmt.Errorf("icon: 帧数为 0")
	}
	if len(data) < 6+count*16 {
		return nil, fmt.Errorf("icon: 条目表被截断（count=%d，需 %d 字节，实有 %d）", count, 6+count*16, len(data))
	}

	entries := make([]icoEntry, 0, count)
	for i := 0; i < count; i++ {
		e := data[6+i*16 : 6+(i+1)*16]
		w, h := int(e[0]), int(e[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		off := int(binary.LittleEndian.Uint32(e[12:16]))
		// size/off 由 u32 转 int：32 位平台可能溢出为负，一并拒绝。
		if size < 0 || off < 0 || off > len(data) || size > len(data)-off {
			return nil, fmt.Errorf("icon: 第 %d 帧越界（off=%d size=%d len=%d）", i, off, size, len(data))
		}
		entries = append(entries, icoEntry{width: w, height: h, payload: data[off : off+size]})
	}
	return entries, nil
}

// pickICOEntry 选帧：最小的 ≥ target（既不放大也不浪费），全都小于 target
// 时取最大的（交由系统缩放）。返回的下标在 entries 非空时恒有效。
func pickICOEntry(entries []icoEntry, target int) int {
	best := -1
	for i, e := range entries {
		if e.width < target {
			continue
		}
		if best < 0 || e.width < entries[best].width {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	largest := 0
	for i, e := range entries {
		if e.width > entries[largest].width {
			largest = i
		}
	}
	return largest
}

// embeddedIconRGBA 解出内嵌资产中距 target 最近的一帧并转成 *image.RGBA，
// 供 winui.IconFromRGBA 生成 HICON。png.Decode 对带 alpha 的帧返回
// *image.NRGBA（直通 alpha），需经 draw 转回预乘的 *image.RGBA。
func embeddedIconRGBA(target int) (*image.RGBA, error) {
	entries, err := parseICO(embeddedIconICO)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("icon: 内嵌资产无帧")
	}
	e := entries[pickICOEntry(entries, target)]
	img, err := png.Decode(bytes.NewReader(e.payload))
	if err != nil {
		return nil, fmt.Errorf("icon: %dpx 帧解码失败: %w", e.width, err)
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba, nil
}

// iconSource 标记托盘图标来自哪一层回退链，用于日志与测试断言。
type iconSource string

const (
	iconSourceConfig   iconSource = "config"
	iconSourceEmbedded iconSource = "embedded"
	iconSourceBrand    iconSource = "brand"
	iconSourceShell    iconSource = "shell"
)

// resolveIcon 按优先级走图标回退链：用户配置路径（空路径直接跳过，
// 不碰文件系统）→ 内嵌 .ico → 程序化品牌标 → shell 通用图标。每一步收到
// 同一个 path，返回 0 表示不可用并继续下探；命中即短路。最后一步无论
// 返回什么都作为结果（shell 兜底是链尾，失败也只能接受）。
func resolveIcon(path string, fromFile, fromEmbedded, fromBrand, fromShell func(string) uintptr) (uintptr, iconSource) {
	if path != "" {
		if h := fromFile(path); h != 0 {
			return h, iconSourceConfig
		}
	}
	if h := fromEmbedded(path); h != 0 {
		return h, iconSourceEmbedded
	}
	if h := fromBrand(path); h != 0 {
		return h, iconSourceBrand
	}
	return fromShell(path), iconSourceShell
}
