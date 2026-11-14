package appinstance

import (
	"golang.org/x/sys/windows"
	"os"
)

func private(st os.FileInfo) bool { return true } // Caller uses a private per-user ACL location.
func lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func replace(a, b string) error {
	x, e := windows.UTF16PtrFromString(a)
	if e != nil {
		return e
	}
	y, e := windows.UTF16PtrFromString(b)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(x, y, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
