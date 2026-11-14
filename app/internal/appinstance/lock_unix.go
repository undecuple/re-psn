//go:build !windows

package appinstance

import (
	"golang.org/x/sys/unix"
	"os"
)

func private(st os.FileInfo) bool { return st.Mode().Perm()&0077 == 0 }
func lock(f *os.File) error       { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlock(f *os.File)           { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func replace(a, b string) error   { return os.Rename(a, b) }
