package main

import (
	"os"
	"sync"
)

// Keep unattended logs bounded to one 4 MiB file and one previous file.
type rollingLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func newRollingLog(path string) (*rollingLog, error) {
	w := &rollingLog{path: path}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	w.file = f
	if st, e := f.Stat(); e == nil {
		w.size = st.Size()
	}
	return w, nil
}
func (w *rollingLog) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(b)) > 4<<20 {
		_ = w.file.Close()
		_ = os.Remove(w.path + ".1")
		_ = os.Rename(w.path, w.path+".1")
		f, e := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			return 0, e
		}
		w.file = f
		w.size = 0
	}
	n, e := w.file.Write(b)
	w.size += int64(n)
	return n, e
}
func (w *rollingLog) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.file.Close() }
