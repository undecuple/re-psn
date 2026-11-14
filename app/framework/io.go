package framework

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func textOK(s string, max int, empty bool) bool {
	if (!empty && s == "") || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func titleOK(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func boundedRead(r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, n+1))
	if err != nil || int64(len(b)) > n {
		return nil, ErrInvalid
	}
	return b, nil
}
func readFile(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("framework: read state: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() > MaxStateBytes {
		return nil, ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return boundedRead(f, MaxStateBytes)
}

// Reject duplicate object keys and excessive nesting before typed decoding.
func strictJSON(b []byte, dst any) error {
	if len(b) > MaxStateBytes || !utf8.Valid(b) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return ErrInvalid
		}
		t, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		v, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch v {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return ErrInvalid
				}
				key, ok := k.(string)
				// encoding/json otherwise accepts case-insensitive aliases of
				// struct fields. All V1 field/map keys are canonical lowercase.
				if !ok || seen[key] || key != strings.ToLower(key) {
					return ErrInvalid
				}
				seen[key] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return ErrInvalid
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrInvalid
	}
	return nil
}
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	// Do not silently chmod an existing user-selected directory. On Unix its
	// existing permissions must already protect the profile's signing seed.
	return checkPrivateDir(st)
}
func lockDir(dir, name string) (*os.File, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, name+".lock")
	if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return f, nil
}

// replaced distinguishes uncertain final durability from a failure before replacement.
func atomicWrite(path string, b []byte) (replaced bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".framework-*")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return false, err
	}
	if err = replaceFile(tmp, path); err != nil {
		return false, err
	}
	if err = syncDir(filepath.Dir(path)); err != nil {
		return true, fmt.Errorf("framework: replacement complete; durability uncertain: %w", err)
	}
	return true, nil
}
