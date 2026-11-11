// Package store keeps the server's leaderboards behind one interface, so the
// backend can change without touching the protocol handlers. Only the memory
// backend exists today; the -db setting that picks a backend is parsed by
// Open.
//
// The rules every backend keeps (and storetest checks): one board per Key;
// each player's best score stands; a board's order is fixed by the first Put
// to it; ties are broken by who got there first (the earlier At ranks
// higher); ranks are 1-based.
package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Key names one board: a title (its NP communication ID) and the board id
// the title uses.
type Key struct {
	Title string
	Board uint32
}

// Entry is one player's score on a board. Comment is at most 64 bytes (the
// handlers check that); callers must not change it after a Put or a read.
type Entry struct {
	OnlineID string
	Score    int64
	Comment  []byte
	At       time.Time
}

// Board is the summary the status page shows.
type Board struct {
	Key     Key
	Entries int
	Leader  Entry // the rank 1 entry; zero when Entries is 0
}

// Store holds the boards. It is safe for concurrent use. Every call takes a
// context, and an error means the request failed: the server answers it with
// an ERROR and carries on. Errors must never hold the connection string.
type Store interface {
	// Put records e on board k, keeping the player's best: a worse score
	// leaves the old entry (and its At) in place. ascending (lower is better)
	// applies only when the Put creates the board. It returns the player's
	// rank after the Put. A backend with a limit on boards or players refuses
	// a new player past it with ErrFull; a player already on the board is
	// never refused for a limit.
	Put(ctx context.Context, k Key, ascending bool, e Entry) (rank uint32, err error)

	// Range returns the board's entry count and up to count entries in rank
	// order, starting offset entries from the top (offset 0 is rank 1). An
	// offset past the end gives no rows. A missing board has total 0.
	Range(ctx context.Context, k Key, offset, count int) (total int, rows []Entry, err error)

	// Lookup returns the board's entry count and, for each of ids (repeats
	// allowed), that player's entry and rank. Rank 0 means no score; its
	// entry is then the zero Entry. Names match exactly.
	Lookup(ctx context.Context, k Key, ids []string) (total int, rows []Entry, ranks []uint32, err error)

	// Boards lists every board with its entry count and leader, in no
	// particular order.
	Boards(ctx context.Context) ([]Board, error)

	// Name is the backend's name, for the log ("memory").
	Name() string

	Close() error
}

// ErrFull is what Put returns when a board or player limit would be passed.
// The score is not recorded.
var ErrFull = errors.New("score store is full: a board or player limit is reached")

// ErrUnknown is the error for a -db setting that names no backend. It holds
// none of the setting's text.
var ErrUnknown = errors.New("not a known storage setting (this version takes off or memory)")

// NotBuiltError is the error for a -db setting that names a backend this
// version does not have. Backend is the scheme only, never what follows it.
type NotBuiltError struct{ Backend string }

func (e *NotBuiltError) Error() string {
	return "the " + e.Backend + " backend is not built yet; this version takes off or memory"
}

// planned are the schemes Open recognises but cannot open yet. Only the
// name from this table goes into an error, never the setting itself.
var planned = []struct{ prefix, name string }{
	{"sqlite:", "sqlite"},
	{"postgres://", "postgres"},
	{"postgresql://", "postgresql"},
	{"mysql://", "mysql"},
}

// Open returns the backend a -db setting names: off (the default) and
// memory both give the memory backend. The setting may be a connection
// string with a password in it, so no error Open returns contains any of it.
func Open(setting string) (Store, error) {
	s := strings.ToLower(strings.TrimSpace(setting))
	switch s {
	case "off", "memory":
		return NewMemory(), nil
	}
	for _, p := range planned {
		if strings.HasPrefix(s, p.prefix) {
			return nil, &NotBuiltError{Backend: p.name}
		}
	}
	return nil, ErrUnknown
}
