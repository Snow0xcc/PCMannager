//go:build ignore

// Command genicon regenerates internal/tray/assets/icon.ico from the
// procedural brand mark (internal/logo).
//
// Run from the repository root:
//
//	go run scripts/genicon.go
//
// The asset is committed, so ordinary builds never run a generation step;
// this script only needs to run again when internal/logo changes. Frames are
// PNG-in-ICO (Vista+) at 16/32/48/64/256 px: 16/32 match the tray small-icon
// slot at common DPIs, 48/64 cover scaled trays, 256 is the largest frame the
// shell ever asks for.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/snow0xcc/pcmannager/internal/logo"
)

var sizes = []int{16, 32, 48, 64, 256}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genicon:", err)
		os.Exit(1)
	}
}

func run() error {
	var payloads [][]byte
	for _, s := range sizes {
		img := logo.Render(s)
		if img == nil {
			return fmt.Errorf("logo.Render(%d) = nil", s)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return fmt.Errorf("PNG 编码 %dpx: %w", s, err)
		}
		payloads = append(payloads, buf.Bytes())
	}

	// ICONDIR: reserved, type (1 = icon), frame count.
	var out bytes.Buffer
	hdr := make([]byte, 6)
	binary.LittleEndian.PutUint16(hdr[2:4], 1)
	binary.LittleEndian.PutUint16(hdr[4:6], uint16(len(sizes)))
	out.Write(hdr)

	// ICONDIRENTRY table follows the header, payloads follow the table.
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0 // ICO 目录用 0 表示 256
		}
		entry := make([]byte, 16)
		entry[0], entry[1] = dim, dim
		binary.LittleEndian.PutUint16(entry[4:6], 1)  // planes
		binary.LittleEndian.PutUint16(entry[6:8], 32) // bit count
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(payloads[i])))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(offset))
		out.Write(entry)
		offset += len(payloads[i])
	}
	for _, p := range payloads {
		out.Write(p)
	}

	dst := filepath.Join("internal", "tray", "assets", "icon.ico")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, out.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes, %d frames)\n", dst, out.Len(), len(sizes))
	return nil
}
