package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/undecuple/re-psn/app/framework"
)

func TestProfileCommandsOfflineAndEncryptedRestore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "profile")
	var out, errs bytes.Buffer
	call := func(args ...string) int { t.Helper(); out.Reset(); errs.Reset(); return run(args, &out, &errs) }
	if code := call("profile", "create", "--dir", dir, "--name", "Synthetic CLI player"); code != 0 {
		t.Fatal(code, errs.String())
	}
	var initial framework.View
	if err := json.Unmarshal(out.Bytes(), &initial); err != nil || initial.AccountID == "" {
		t.Fatal("profile output", err)
	}
	if strings.Contains(out.String(), "seed") || strings.Contains(out.String(), "private_key") {
		t.Fatal("secret in profile output")
	}
	catalog := filepath.Join(root, "catalog.json")
	data := `{"title_id":"SYNTH-CLI","name":"Synthetic CLI catalog","revision":"1","trophies":[{"id":1,"name":"Invented fixture","description":"Synthetic only","grade":"bronze"}]}`
	if err := os.WriteFile(catalog, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if code := call("trophies", "install", "--dir", dir, "--input", catalog); code != 0 {
		t.Fatal(code, errs.String())
	}
	if code := call("trophies", "show", "--dir", dir); code != 0 || !strings.Contains(out.String(), "SYNTH-CLI") {
		t.Fatal("catalog show", code, errs.String())
	}
	password := filepath.Join(root, "passphrase")
	if err := os.WriteFile(password, []byte("synthetic CLI passphrase\n"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.rpp")
	if code := call("profile", "export", "--dir", dir, "--output", archive, "--passphrase-file", password); code != 0 {
		t.Fatal(code, errs.String())
	}
	encrypted, err := os.ReadFile(archive)
	if err != nil || bytes.Contains(encrypted, []byte(initial.AccountID)) || bytes.Contains(encrypted, []byte("Synthetic CLI player")) {
		t.Fatal("plaintext archive", err)
	}
	before := append([]byte(nil), encrypted...)
	if code := call("profile", "export", "--dir", dir, "--output", archive, "--passphrase-file", password); code == 0 {
		t.Fatal("export replaced an existing backup")
	}
	after, _ := os.ReadFile(archive)
	if !bytes.Equal(before, after) {
		t.Fatal("failed export changed old backup")
	}
	restored := filepath.Join(root, "restored")
	if code := call("profile", "restore", "--dir", restored, "--input", archive, "--passphrase-file", password); code != 0 {
		t.Fatal(code, errs.String())
	}
	var view framework.View
	if err := json.Unmarshal(out.Bytes(), &view); err != nil || view.AccountID != initial.AccountID || len(view.Titles) != 1 {
		t.Fatal("restore identity/catalog", err)
	}
	if code := call("profile", "restore", "--dir", restored, "--input", archive, "--passphrase-file", password); code == 0 {
		t.Fatal("restore overwrote an existing profile")
	}
	if code := call("profile", "show", "--dir", restored); code != 0 {
		t.Fatal(code, errs.String())
	}
	if code := call("friends", "list", "--dir", dir, "--relay", "http://127.0.0.1:1"); code == 0 {
		t.Fatal("plaintext social origin accepted")
	}
}

func TestCatalogInputDoesNotOverrideDuplicateFields(t *testing.T) {
	root := t.TempDir()
	s, err := framework.Create(filepath.Join(root, "p"), "Synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	path := filepath.Join(root, "bad.json")
	if err := os.WriteFile(path, []byte(`{"title_id":"A","title_id":"B","name":"Synthetic","revision":"1","trophies":[{"id":1,"name":"Fixture","description":"","grade":"bronze"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"trophies", "install", "--dir", filepath.Join(root, "p"), "--input", path}, &out, &errs); code == 0 {
		t.Fatal("duplicate catalog field accepted")
	}
	v, err := framework.Read(filepath.Join(root, "p"))
	if err != nil || len(v.Titles) != 0 {
		t.Fatal("bad catalog mutated profile", err)
	}
}
