//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO, or a symlink to one, would block in open; the loaders refuse it before opening.
func TestPassFilesRefuseAFIFOBehindASymlink(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Skip(err)
	}
	for _, path := range []string{fifo, link} {
		done := make(chan error, 1)
		go func() { _, err := loadPassKeys(path); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("%s accepted", path)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: the loader blocked", path)
		}
	}
}
