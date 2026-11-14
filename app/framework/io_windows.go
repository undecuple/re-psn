package framework

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows mode bits do not establish private ACLs. The caller chooses a private
// per-user location; this implementation makes no ACL isolation claim.
func checkPrivateDir(os.FileInfo) error { return nil }

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func unlockFile(f *os.File) error {
	err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
	ce := f.Close()
	if err != nil {
		return err
	}
	return ce
}
func replaceFile(src, dst string) error {
	s, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	d, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(s, d, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx WRITE_THROUGH supplies the supported Windows replacement durability request.
func syncDir(string) error { return nil }
