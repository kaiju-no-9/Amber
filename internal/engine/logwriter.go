package engine

import (
	"fmt"
	"os"
	"sync"
)

const maxLogBytes int64 = 10 << 20

type rotatingLogWriter struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	maxSize int64
}

func openRotatingLogWriter(path string) (*rotatingLogWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	writer := &rotatingLogWriter{path: path, file: file, size: info.Size(), maxSize: maxLogBytes}
	if writer.size >= writer.maxSize {
		if err := writer.rotate(); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	return writer, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	total := 0
	for len(p) > 0 {
		if w.size >= w.maxSize {
			if err := w.rotate(); err != nil {
				return total, err
			}
		}
		remaining := w.maxSize - w.size
		chunkSize := int64(len(p))
		if chunkSize > remaining {
			chunkSize = remaining
		}
		written, err := w.file.Write(p[:int(chunkSize)])
		total += written
		w.size += int64(written)
		p = p[written:]
		if err != nil {
			return total, err
		}
		if written == 0 {
			return total, fmt.Errorf("write log: no progress")
		}
	}
	return total, nil
}

func (w *rotatingLogWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close current log: %w", err)
	}
	backupPath := w.path + ".1"
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove previous log backup: %w", err)
	}
	if err := os.Rename(w.path, backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate log: %w", err)
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create new log: %w", err)
	}
	w.file = file
	w.size = 0
	return nil
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
