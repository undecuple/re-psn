package main

import (
	"bytes"
	"github.com/undecuple/re-psn/app/internal/appinstance"
	"os"
	"path/filepath"
	"testing"
)

func TestExistingLaunchFileFailsCleanlyAndReleasesInstance(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "private", "re-psn.ini")
	link := filepath.Join(root, "existing.url")
	const sentinel = "existing caller data"
	if err := os.WriteFile(link, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := runUI([]string{"--config", config, "--no-open", "--url-file", link}, &out, &errs); code != 1 {
		t.Fatal("expected controlled launch failure", code)
	}
	b, err := os.ReadFile(link)
	if err != nil || string(b) != sentinel {
		t.Fatal("failed launch removed or changed caller file", err)
	}
	lock, owner, err := appinstance.Acquire(config)
	if err != nil || !owner {
		t.Fatal("failed launch leaked instance ownership", err)
	}
	lock.Close()
}
func TestHeadlessUIDoesNotPrintLaunchSecret(t *testing.T) {
	var out, errs bytes.Buffer
	if code := runUI([]string{"--no-open"}, &out, &errs); code != 2 || out.Len() != 0 {
		t.Fatal("headless UI needs explicit private file", code, out.Len())
	}
}
