// Package framework provides local profiles, player-reported trophies and opt-in
// authenticated social accounts. Its game capability never exposes identity keys.
package framework

import "errors"

var (
	ErrBusy         = errors.New("framework: state is busy")
	ErrNotFound     = errors.New("framework: not found")
	ErrExists       = errors.New("framework: destination exists")
	ErrInvalid      = errors.New("framework: invalid input")
	ErrUnauthorized = errors.New("framework: unavailable or unauthorized")
	ErrClosed       = errors.New("framework: closed")
)

const (
	MaxStateBytes = 4 << 20
	MaxTitles     = 128
	MaxTrophies   = 1024
	MaxAccounts   = 256
	MaxContacts   = 64
)

type Trophy struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Grade       string `json:"grade"`
}
type Catalog struct {
	TitleID  string   `json:"title_id"`
	Name     string   `json:"name"`
	Revision string   `json:"revision"`
	Trophies []Trophy `json:"trophies"`
}
type Unlock struct {
	TrophyID   uint32 `json:"trophy_id"`
	Unlocked   bool   `json:"unlocked"`
	UnlockedAt int64  `json:"unlocked_at"` // Unix milliseconds; local first report, not verified gameplay time.
}
type TitleView struct {
	Catalog Catalog  `json:"catalog"`
	Unlocks []Unlock `json:"unlocks"`
}
type RelayView struct {
	Origin    string      `json:"origin"`
	FetchedAt int64       `json:"fetched_at"` // Unix milliseconds; cached remote state can be stale.
	State     SocialState `json:"state"`
}
type View struct {
	AccountID string      `json:"account_id"`
	Label     string      `json:"label"`
	Titles    []TitleView `json:"titles"`
	Relays    []RelayView `json:"relays"`
}
type Contact struct {
	AccountID string `json:"account_id"`
	Label     string `json:"label"`
}
type SocialState struct {
	AccountID string    `json:"account_id"`
	Label     string    `json:"label"`
	Revision  uint64    `json:"revision"`
	Friends   []Contact `json:"friends"`
	Incoming  []Contact `json:"incoming"`
	Outgoing  []Contact `json:"outgoing"`
	Blocked   []string  `json:"blocked"`
}
