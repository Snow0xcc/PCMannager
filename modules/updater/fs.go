// Package updater — local-file side: capped copy, atomic replace, checksum.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyCapped streams r into dst atomically: data is written to a temp file in
// the same directory, capped at cap, hashed on the way through, then renamed
// over dst. The rename gives crash-atomicity — the destination is either the
// previous file or a complete new file, never a truncated partial download.
func copyCapped(dst string, r io.Reader, capBytes int64) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".updater-*")
	if err != nil {
		return 0, "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, capBytes+1))
	if err != nil {
		tmp.Close()
		return 0, "", err
	}
	if n > capBytes {
		tmp.Close()
		return n, "", fmt.Errorf("updater: 下载超过大小上限 %d MiB", capBytes>>20)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return n, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
