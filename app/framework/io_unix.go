//go:build !windows

package framework

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func checkPrivateDir(st os.FileInfo) error {
	if st.Mode().Perm()&0077 != 0 {
		return ErrInvalid
	}
	return nil
}
func unlockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	ce := f.Close()
	if err != nil {
		return err
	}
	return ce
}
func replaceFile(src, dst string) error { return os.Rename(src, dst) }
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
