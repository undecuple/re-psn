package appinstance

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestInstanceFailedPublishRetainsOwnedIdentity(t *testing.T) {
	owner, yes, err := Acquire(privateConfig(t))
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	defer owner.Close()
	if err := owner.Publish(Locator{Profile: "first"}); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(owner.path)
	if err != nil {
		t.Fatal(err)
	}
	// Deny replacement while the original publication is open, then release
	// the reader so Close can remove that still-owned original publication.
	handle, err := windows.CreateFile(path, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = owner.Publish(Locator{Profile: "second"})
	closeErr := windows.CloseHandle(handle)
	if err == nil {
		t.Fatal("publication replacement succeeded despite denied delete sharing")
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	owner.Close()
	if _, err := os.Lstat(owner.path); !os.IsNotExist(err) {
		t.Fatalf("failed Publish lost the previous owned identity: %v", err)
	}
}
