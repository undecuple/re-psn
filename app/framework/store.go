package framework

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type profileData struct {
	Version int    `json:"version"`
	Seed    []byte `json:"seed"`
	View    View   `json:"view"`
}
type Store struct {
	mu         sync.Mutex
	dir        string
	lock       *os.File
	data       profileData
	closed     bool
	socialGate chan struct{}
}

// DecodeCatalog accepts one bounded, strict catalog document.
func DecodeCatalog(r io.Reader) (Catalog, error) {
	var c Catalog
	b, err := boundedRead(r, MaxStateBytes)
	if err != nil || strictJSON(b, &c) != nil || !validCatalog(c) {
		return Catalog{}, ErrInvalid
	}
	return c, nil
}

func accountID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "rp1-" + hex.EncodeToString(h[:])
}
func idOK(id string) bool {
	if len(id) != 68 || !strings.HasPrefix(id, "rp1-") {
		return false
	}
	b, err := hex.DecodeString(id[4:])
	return err == nil && len(b) == 32 && id == strings.ToLower(id)
}
func clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func Create(dir, label string) (*Store, error) {
	if !textOK(label, 128, false) {
		return nil, ErrInvalid
	}
	f, err := lockDir(dir, "profile")
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(dir, "profile.json")); !os.IsNotExist(err) {
		unlockFile(f)
		return nil, ErrExists
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		unlockFile(f)
		return nil, err
	}
	s := &Store{dir: dir, lock: f, socialGate: make(chan struct{}, 1), data: profileData{Version: 1, Seed: key.Seed(), View: View{AccountID: accountID(key.Public().(ed25519.PublicKey)), Label: label}}}
	if err := s.commit(s.data); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func Open(dir string) (*Store, error) {
	f, err := lockDir(dir, "profile")
	if err != nil {
		return nil, err
	}
	d, err := loadProfile(dir)
	if err != nil {
		unlockFile(f)
		return nil, err
	}
	return &Store{dir: dir, lock: f, data: d, socialGate: make(chan struct{}, 1)}, nil
}
func loadProfile(dir string) (profileData, error) {
	var d profileData
	b, err := readFile(filepath.Join(dir, "profile.json"))
	if err != nil {
		return d, err
	}
	if err := strictJSON(b, &d); err != nil {
		return d, err
	}
	return d, validateProfile(d)
}
func Read(dir string) (View, error) { d, err := loadProfile(dir); return clone(d.View), err }
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	clear(s.data.Seed)
	return unlockFile(s.lock)
}
func (s *Store) View() View { s.mu.Lock(); defer s.mu.Unlock(); return clone(s.data.View) }
func validCatalog(c Catalog) bool {
	if !titleOK(c.TitleID) || !textOK(c.Name, 128, false) || !textOK(c.Revision, 64, false) || len(c.Trophies) < 1 || len(c.Trophies) > MaxTrophies {
		return false
	}
	seen := map[uint32]bool{}
	for _, t := range c.Trophies {
		if seen[t.ID] || !textOK(t.Name, 128, false) || !textOK(t.Description, 1024, true) {
			return false
		}
		seen[t.ID] = true
		switch t.Grade {
		case "bronze", "silver", "gold", "platinum":
		default:
			return false
		}
	}
	return true
}
func validateProfile(d profileData) error {
	if d.Version != 1 || len(d.Seed) != ed25519.SeedSize || !textOK(d.View.Label, 128, false) || len(d.View.Titles) > MaxTitles || len(d.View.Relays) > 16 {
		return ErrInvalid
	}
	key := ed25519.NewKeyFromSeed(d.Seed)
	if d.View.AccountID != accountID(key.Public().(ed25519.PublicKey)) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, t := range d.View.Titles {
		if seen[t.Catalog.TitleID] || !validCatalog(t.Catalog) || len(t.Unlocks) > len(t.Catalog.Trophies) {
			return ErrInvalid
		}
		seen[t.Catalog.TitleID] = true
		ids := map[uint32]bool{}
		for _, def := range t.Catalog.Trophies {
			ids[def.ID] = true
		}
		us := map[uint32]bool{}
		for _, u := range t.Unlocks {
			if !ids[u.TrophyID] || us[u.TrophyID] || !u.Unlocked || u.UnlockedAt <= 0 {
				return ErrInvalid
			}
			us[u.TrophyID] = true
		}
	}
	seen = map[string]bool{}
	for _, r := range d.View.Relays {
		if seen[r.Origin] || !validOrigin(r.Origin) || r.FetchedAt <= 0 || r.State.AccountID != d.View.AccountID || !validSocialState(r.State) {
			return ErrInvalid
		}
		seen[r.Origin] = true
	}
	return nil
}
func (s *Store) commit(next profileData) error {
	if s.closed {
		return ErrClosed
	}
	if err := validateProfile(next); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil || len(b) > MaxStateBytes {
		return ErrInvalid
	}
	replaced, err := atomicWrite(filepath.Join(s.dir, "profile.json"), b)
	if replaced {
		s.data = next
	}
	return err
}
func (s *Store) InstallCatalog(c Catalog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !validCatalog(c) {
		return ErrInvalid
	}
	next := clone(s.data)
	for _, t := range next.View.Titles {
		if t.Catalog.TitleID == c.TitleID {
			if reflect.DeepEqual(t.Catalog, c) {
				return nil
			}
			return ErrExists
		}
	}
	if len(next.View.Titles) >= MaxTitles {
		return ErrInvalid
	}
	next.View.Titles = append(next.View.Titles, TitleView{Catalog: clone(c)})
	sort.Slice(next.View.Titles, func(i, j int) bool { return next.View.Titles[i].Catalog.TitleID < next.View.Titles[j].Catalog.TitleID })
	return s.commit(next)
}
func status(d profileData, title string, id uint32) (Unlock, error) {
	for _, t := range d.View.Titles {
		if t.Catalog.TitleID != title {
			continue
		}
		found := false
		for _, def := range t.Catalog.Trophies {
			if def.ID == id {
				found = true
				break
			}
		}
		if !found {
			return Unlock{}, ErrNotFound
		}
		for _, u := range t.Unlocks {
			if u.TrophyID == id {
				return u, nil
			}
		}
		return Unlock{TrophyID: id}, nil
	}
	return Unlock{}, ErrNotFound
}
func (s *Store) Status(title string, id uint32) (Unlock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Unlock{}, ErrClosed
	}
	return status(s.data, title, id)
}
func (s *Store) Unlock(title string, id uint32) (Unlock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Unlock{}, ErrClosed
	}
	u, err := status(s.data, title, id)
	if err != nil || u.Unlocked {
		return u, err
	}
	next := clone(s.data)
	u.Unlocked = true
	u.UnlockedAt = time.Now().UnixMilli()
	for i := range next.View.Titles {
		if next.View.Titles[i].Catalog.TitleID == title {
			next.View.Titles[i].Unlocks = append(next.View.Titles[i].Unlocks, u)
			break
		}
	}
	return u, s.commit(next)
}
func (s *Store) key() (ed25519.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	return ed25519.NewKeyFromSeed(bytes.Clone(s.data.Seed)), nil
}
func (s *Store) cache(origin string, state SocialState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.data)
	r := RelayView{Origin: origin, FetchedAt: time.Now().UnixMilli(), State: clone(state)}
	for i := range next.View.Relays {
		if next.View.Relays[i].Origin == origin {
			next.View.Relays[i] = r
			return s.commit(next)
		}
	}
	if len(next.View.Relays) >= 16 {
		return ErrInvalid
	}
	next.View.Relays = append(next.View.Relays, r)
	return s.commit(next)
}
