package framework

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private-state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureCatalog(title string) Catalog {
	return Catalog{TitleID: title, Name: "Synthetic trophy laboratory", Revision: "1", Trophies: []Trophy{{ID: 7, Name: "Synthetic first report", Description: "Generated fixture", Grade: "bronze"}, {ID: 9, Name: "Synthetic second report", Grade: "gold"}}}
}
func fixtureStore(t *testing.T, label string) *Store {
	t.Helper()
	s, err := Create(privateTempDir(t), label)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestStoreOfflinePersistenceAndIsolation(t *testing.T) {
	dir := privateTempDir(t)
	s, err := Create(dir, "Synthetic player")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InstallCatalog(fixtureCatalog("SYNTHETIC-A")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unlock("SYNTHETIC-B", 7); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Unlock("SYNTHETIC-A", 8); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan Unlock, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := s.Unlock("SYNTHETIC-A", 7)
			if err != nil {
				t.Error(err)
			}
			results <- u
		}()
	}
	wg.Wait()
	close(results)
	var first Unlock
	for u := range results {
		if !u.Unlocked || u.UnlockedAt <= 0 {
			t.Fatal(u)
		}
		if first.UnlockedAt != 0 && u != first {
			t.Fatalf("non-idempotent: %v %v", u, first)
		}
		first = u
	}
	c := fixtureCatalog("SYNTHETIC-A")
	c.Trophies[0].Name = "Reassigned"
	if err := s.InstallCatalog(c); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	view, err := Read(dir)
	if err != nil || len(view.Titles) != 1 || len(view.Titles[0].Unlocks) != 1 {
		t.Fatal(view, err)
	}
	id := view.AccountID
	view.Titles[0].Catalog.Name = "mutated copy"
	if s.View().Titles[0].Catalog.Name == view.Titles[0].Catalog.Name {
		t.Fatal("view aliases store")
	}
	if _, err := Open(dir); !errors.Is(err, ErrBusy) {
		t.Fatal("second writer", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.Status("SYNTHETIC-A", 7)
	if err != nil || u != first || s.View().AccountID != id {
		t.Fatal(u, err)
	}
	if st, err := os.Stat(filepath.Join(dir, "profile.json")); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		t.Fatal("state permissions", st, err)
	}
}
func TestProcessExclusiveWriter(t *testing.T) {
	if dir := os.Getenv("RE_PSN_TEST_LOCK_DIR"); dir != "" {
		s, err := Open(dir)
		if errors.Is(err, ErrBusy) {
			os.Exit(0)
		}
		if s != nil {
			s.Close()
		}
		os.Exit(3)
	}
	s := fixtureStore(t, "Synthetic lock player")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessExclusiveWriter$")
	cmd.Env = append(os.Environ(), "RE_PSN_TEST_LOCK_DIR="+s.dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("other process acquired writer: %s: %v", out, err)
	}
}
func TestFailedReplacementDoesNotClaimUnlock(t *testing.T) {
	s := fixtureStore(t, "Synthetic failure player")
	if err := s.InstallCatalog(fixtureCatalog("SYNTHETIC")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.dir, "profile.json")
	backup := p + ".saved"
	if err := os.Rename(p, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unlock("SYNTHETIC", 7); err == nil {
		t.Fatal("write failure reported success")
	}
	u, err := s.Status("SYNTHETIC", 7)
	if err != nil || u.Unlocked {
		t.Fatal("failed mutation changed memory", u, err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, p); err != nil {
		t.Fatal(err)
	}
	v, err := Read(s.dir)
	if err != nil || len(v.Titles[0].Unlocks) != 0 {
		t.Fatal("old state not preserved", v, err)
	}
}
func TestArchiveRestoreBoundaries(t *testing.T) {
	s := fixtureStore(t, "Synthetic portable player")
	if err := s.InstallCatalog(fixtureCatalog("SYNTHETIC")); err != nil {
		t.Fatal(err)
	}
	u, err := s.Unlock("SYNTHETIC", 7)
	if err != nil {
		t.Fatal(err)
	}
	p := []byte("synthetic passphrase for tests")
	var out bytes.Buffer
	if err := s.Export(&out, p); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte(s.View().AccountID)) {
		t.Fatal("plaintext identity in archive")
	}
	dest := filepath.Join(privateTempDir(t), "restored")
	r, err := Restore(dest, bytes.NewReader(out.Bytes()), p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := r.Status("SYNTHETIC", 7)
	if err != nil || got != u || r.View().AccountID != s.View().AccountID {
		t.Fatal(got, err)
	}
	if _, err := Restore(dest, bytes.NewReader(out.Bytes()), p); !errors.Is(err, ErrExists) {
		t.Fatal("overwrite", err)
	}
	for _, which := range []string{"password", "header", "ciphertext", "oversize", "short"} {
		t.Run(which, func(t *testing.T) {
			data := bytes.Clone(out.Bytes())
			pass := p
			switch which {
			case "password":
				pass = []byte("incorrect synthetic passphrase")
			case "header":
				data[7] = '2'
			case "ciphertext":
				data[len(data)-1] ^= 1
			case "oversize":
				data = make([]byte, MaxStateBytes+archiveHeader+17)
			case "short":
				pass = []byte("short")
			}
			target := filepath.Join(privateTempDir(t), "rejected")
			if _, err := Restore(target, bytes.NewReader(data), pass); err == nil {
				t.Fatal("accepted invalid archive")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("failed validation wrote destination", err)
			}
		})
	}
}
func TestStrictStateValidation(t *testing.T) {
	s := fixtureStore(t, "Synthetic schema player")
	s.mu.Lock()
	b, err := json.Marshal(s.data)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{"version":1,"version":1}`), []byte(`{"version":1,"Version":1}`), []byte(`{"x":1}`), []byte(strings.Repeat("[", 34) + strings.Repeat("]", 34)), {0xff}} {
		var d profileData
		if strictJSON(bad, &d) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	var d profileData
	if err := strictJSON(b, &d); err != nil {
		t.Fatal(err)
	}
	d.Seed[0] ^= 1
	if validateProfile(d) == nil {
		t.Fatal("accepted mismatched seed")
	}
	c := fixtureCatalog("SYNTHETIC")
	c.Trophies = append(c.Trophies, c.Trophies[0])
	if validCatalog(c) {
		t.Fatal("duplicate trophy IDs")
	}
}

func TestDecodeCatalogBoundaries(t *testing.T) {
	b, err := json.Marshal(fixtureCatalog("SYNTHETIC"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCatalog(bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(b, []byte(`"title_id":`), []byte(`"title_id":"OTHER","title_id":`), 1)
	unknown := append(bytes.Clone(b[:len(b)-1]), []byte(`,"unknown":true}`)...)
	for _, bad := range [][]byte{duplicate, unknown, append(bytes.Clone(b), []byte(` {}`)...), make([]byte, MaxStateBytes+1)} {
		if _, err := DecodeCatalog(bytes.NewReader(bad)); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid catalog", err)
		}
	}
}
