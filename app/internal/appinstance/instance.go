// Package appinstance keeps one UI controller per configuration and authenticates
// reopening it without putting a persistent browser capability in a launch URL.
package appinstance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Locator struct {
	Origin  string `json:"origin"`
	Key     string `json:"key"`
	Profile string `json:"profile"`
	Binary  string `json:"binary"`
}
type Instance struct {
	file      *os.File
	path      string
	published os.FileInfo
}

func Canonical(path string) string {
	if path == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(Canonical(parent), filepath.Base(path))
}
func Acquire(configPath string) (*Instance, bool, error) {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, false, err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || !private(st) {
		return nil, false, errors.New("app settings directory must be private to its owner")
	}
	basename := filepath.Base(configPath)
	if runtime.GOOS == "windows" {
		basename = strings.ToLower(basename)
	}
	sum := sha256.Sum256([]byte(basename))
	name := "ui-" + hex.EncodeToString(sum[:8])
	lockPath := filepath.Join(dir, name+".lock")
	if st, err := os.Lstat(lockPath); err == nil && !st.Mode().IsRegular() {
		return nil, false, errors.New("invalid app lock file")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	instance := &Instance{file: f, path: filepath.Join(dir, name+".json")}
	if err = lock(f); err != nil {
		f.Close()
		instance.file = nil
		return instance, false, nil
	}
	return instance, true, nil
}
func (i *Instance) Publish(value Locator) error {
	if i.file == nil {
		return errors.New("not the UI owner")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(i.path), ".ui-instance-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = replace(f.Name(), i.path); err != nil {
		return err
	}
	i.published, err = os.Lstat(i.path)
	return err
}
func (i *Instance) Close() {
	if i.file == nil {
		return
	}
	if current, err := os.Lstat(i.path); err == nil && i.published != nil && os.SameFile(current, i.published) {
		_ = os.Remove(i.path)
	}
	unlock(i.file)
	i.file.Close()
	i.file = nil
}
func Proof(key, domain, value string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte("re-psn-ui-" + domain + "-v1\n" + value))
	return hex.EncodeToString(h.Sum(nil))
}
func Equal(a, b string) bool {
	x, e := hex.DecodeString(a)
	y, f := hex.DecodeString(b)
	return e == nil && f == nil && len(x) == 32 && len(y) == 32 && hmac.Equal(x, y)
}
func ValidLink(link, origin string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.RawQuery != "" || u.Path != "/" || u.RawPath != "" || origin != "http://"+u.Host {
		return false
	}
	raw := strings.TrimPrefix(u.Fragment, "token=")
	token, e := hex.DecodeString(raw)
	return strings.HasPrefix(u.Fragment, "token=") && e == nil && len(token) == 32 && len(raw) == 64
}
func (i *Instance) Reopen(ctx context.Context, profile, binary string) (string, error) {
	st, err := os.Lstat(i.path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16384 || !private(st) {
		return "", errors.New("running app launch information unavailable; try again shortly")
	}
	b, err := os.ReadFile(i.path)
	if err != nil {
		return "", err
	}
	var loc Locator
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&loc) != nil || loc.Profile != profile || loc.Binary != binary {
		return "", errors.New("app already running with different launch settings")
	}
	if !ValidLink(loc.Origin+"/#token="+strings.Repeat("0", 64), loc.Origin) || len(loc.Key) != 64 {
		return "", errors.New("invalid running app information")
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return "", err
	}
	challenge := hex.EncodeToString(nonce[:])
	body, _ := json.Marshal(map[string]string{"challenge": challenge, "proof": Proof(loc.Key, "request", challenge)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loc.Origin+"/api/reopen", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("running app did not answer")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2049))
	if err != nil || len(raw) > 2048 || res.StatusCode != 200 {
		return "", errors.New("running app refused to reopen")
	}
	var reply struct {
		URL   string `json:"url"`
		Proof string `json:"proof"`
	}
	if json.Unmarshal(raw, &reply) != nil || !ValidLink(reply.URL, loc.Origin) || !Equal(reply.Proof, Proof(loc.Key, "response", challenge+"\n"+reply.URL)) {
		return "", errors.New("running app response could not be authenticated")
	}
	return reply.URL, nil
}
