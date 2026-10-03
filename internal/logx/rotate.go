package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Rotation defaults.
//
// A long-running tray application logs continuously, so an unbounded file is a
// slow disk-space leak (PRD NFR "log rotation"). These bounds keep the total
// footprint per sink at roughly (backups+1) × maxLogBytes.
const (
	// defaultMaxLogBytes is the size at which a log file is rolled over. 8 MiB
	// holds several days of ordinary operation at info level while staying small
	// enough to attach to a bug report.
	defaultMaxLogBytes = 8 << 20
	// defaultMaxLogBackups is how many rolled files are kept: gobox.log.1 ..
	// gobox.log.N. Three is enough to cover "it broke yesterday and today".
	defaultMaxLogBackups = 3
)

// rotatingWriter is an io.Writer that rolls a log file over once it reaches a
// size limit, keeping a bounded number of numbered backups.
//
// Rotation happens at the writer rather than in an external log library because
// the project is deliberately dependency-light and the requirement is simply
// "the file must not grow forever". The scheme is the conventional one:
// gobox.log.2 → gobox.log.3, gobox.log.1 → gobox.log.2, gobox.log → gobox.log.1.
type rotatingWriter struct {
	path       string
	maxBytes   int64
	maxBackups int

	// mu guards size and file. slog handlers may be invoked from several
	// goroutines, and a rotation that ran concurrently with a write would lose
	// records or write to a closed handle.
	mu   sync.Mutex
	file *os.File
	size int64
	// err remembers a write failure so the caller can observe a broken sink
	// without the logger becoming fatal.
	err error
}

// newRotatingWriter opens (or creates) path and returns a writer that keeps it
// bounded to maxBytes with maxBackups rolled files.
//
// maxBytes/maxBackups <= 0 fall back to the defaults, so a zero Options value
// still gets rotation rather than an unbounded file.
func newRotatingWriter(path string, maxBytes int64, maxBackups int) (*rotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxLogBytes
	}
	if maxBackups <= 0 {
		maxBackups = defaultMaxLogBackups
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	rw := &rotatingWriter{
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		file:       f,
	}
	if fi, err := f.Stat(); err == nil {
		rw.size = fi.Size()
	}
	return rw, nil
}

// Write appends p, rolling the file over first when it would exceed the limit.
func (r *rotatingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return 0, fmt.Errorf("logx: 日志文件已关闭")
	}
	// Roll before writing, so a single oversized record still lands in a fresh
	// file rather than being split across two.
	if r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotateLocked(); err != nil {
			r.err = err
			// Keep writing to the existing handle: losing the app's logs because a
			// rename failed would be far worse than an oversized file.
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	if err != nil {
		r.err = err
	}
	return n, err
}

// rotateLocked closes the current file and shifts the backups. Caller holds mu.
func (r *rotatingWriter) rotateLocked() error {
	if err := r.file.Close(); err != nil {
		// The handle is unusable either way; report and continue to the rename so
		// the next open can succeed.
		r.err = err
	}
	r.file = nil

	// Drop the oldest backup, then shift the rest up by one. os.Rename fails on
	// Windows when the destination exists, so each destination is removed first.
	oldest := backupPath(r.path, r.maxBackups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := r.maxBackups - 1; i >= 1; i-- {
		src, dst := backupPath(r.path, i), backupPath(r.path, i+1)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	if err := os.Rename(r.path, backupPath(r.path, 1)); err != nil {
		return err
	}

	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	r.file = f
	r.size = 0
	return nil
}

// Close releases the underlying file. It is idempotent.
func (r *rotatingWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// backupPath returns the i-th backup of path ("gobox.log" → "gobox.log.1").
func backupPath(path string, i int) string {
	return path + "." + strconv.Itoa(i)
}
