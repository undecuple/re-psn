package framework

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const archiveHeader = 36 // RPSNBAK1 + salt16 + nonce12; V1 fixes the KDF at 600000.
const archiveIterations = 600000

func passwordOK(p []byte) bool { return len(p) >= 12 && len(p) <= 1024 }
func archiveAEAD(p, salt []byte) (cipher.AEAD, error) {
	k, err := pbkdf2.Key(sha256.New, string(p), salt, archiveIterations, 32)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(k)
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Store) Export(w io.Writer, passphrase []byte) error {
	if !passwordOK(passphrase) {
		return ErrInvalid
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	d := clone(s.data)
	s.mu.Unlock()
	defer clear(d.Seed)
	// Remote state is an authority cache, never portable relationship acceptance.
	d.View.Relays = nil
	b, err := json.Marshal(d)
	if err != nil || len(b) > MaxStateBytes {
		return ErrInvalid
	}
	defer clear(b)
	header := make([]byte, archiveHeader)
	copy(header, "RPSNBAK1")
	if _, err := rand.Read(header[8:]); err != nil {
		return err
	}
	a, err := archiveAEAD(passphrase, header[8:24])
	if err != nil {
		return err
	}
	out := a.Seal(header, header[24:36], b, header)
	n, err := w.Write(out)
	if err == nil && n != len(out) {
		return io.ErrShortWrite
	}
	return err
}
func Restore(dir string, r io.Reader, passphrase []byte) (*Store, error) {
	if !passwordOK(passphrase) {
		return nil, ErrInvalid
	}
	b, err := boundedRead(r, MaxStateBytes+archiveHeader+16)
	if err != nil || len(b) < archiveHeader+16 || !bytes.Equal(b[:8], []byte("RPSNBAK1")) {
		return nil, ErrInvalid
	}
	a, err := archiveAEAD(passphrase, b[8:24])
	if err != nil {
		return nil, ErrInvalid
	}
	plain, err := a.Open(nil, b[24:36], b[36:], b[:36])
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(plain)
	var d profileData
	if err := strictJSON(plain, &d); err != nil {
		return nil, err
	}
	if err := validateProfile(d); err != nil {
		return nil, err
	}
	d.View.Relays = nil
	// Never merge or replace a profile. Validation precedes any destination writes.
	if st, err := os.Lstat(dir); err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, ErrExists
		}
		es, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		if len(es) != 0 {
			return nil, ErrExists
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := lockDir(dir, "profile")
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, lock: f, data: d, socialGate: make(chan struct{}, 1)}
	if _, err := os.Lstat(filepath.Join(dir, "profile.json")); !os.IsNotExist(err) {
		s.Close()
		return nil, ErrExists
	}
	if err := s.commit(d); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
