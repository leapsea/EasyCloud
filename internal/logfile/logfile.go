// Package logfile writes logs to <dir>/easycloud.log with size-based rotation
// (easycloud.log.1 … .N). It exists for deployments that mount a log directory;
// stdout stays the primary sink.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const name = "easycloud.log"

type Writer struct {
	mu      sync.Mutex
	dir     string
	maxSize int64
	keep    int
	f       *os.File
	size    int64
}

// Open opens (appending) dir/easycloud.log. dir must already exist: a missing
// directory means "no file logging", which the caller decides about.
func Open(dir string, maxSize int64, keep int) (*Writer, error) {
	w := &Writer{dir: dir, maxSize: maxSize, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) path() string { return filepath.Join(w.dir, name) }

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate shifts easycloud.log -> .1 -> .2 … dropping the oldest.
func (w *Writer) rotate() error {
	w.f.Close()
	w.f = nil
	os.Remove(fmt.Sprintf("%s.%d", w.path(), w.keep))
	for i := w.keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", w.path(), i), fmt.Sprintf("%s.%d", w.path(), i+1))
	}
	if w.keep > 0 {
		os.Rename(w.path(), w.path()+".1")
	} else {
		os.Remove(w.path())
	}
	return w.open()
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
