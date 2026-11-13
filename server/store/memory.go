package store

import (
	"context"
	"sort"
	"sync"
)

// The memory backend's limits: a Put that would go past one returns ErrFull.
// An update of a player already on the board is never refused.
const (
	maxTitles          = 1000  // titles that may have boards
	maxBoardsPerTitle  = 1000  // boards one title may have
	maxEntriesPerBoard = 10000 // players on one board
)

// memory keeps the boards in maps, behind one lock. Everything goes with the
// process: a restart empties every board.
type memory struct {
	mu     sync.Mutex
	boards map[Key]*board
	titles map[string]int // boards per title

	// The limits are fields so tests can lower them; NewMemory sets the defaults.
	maxTitles          int
	maxBoardsPerTitle  int
	maxEntriesPerBoard int
}

type board struct {
	ascending bool // lower is better (lap times)
	entries   map[string]*Entry
	order     []*Entry // every entry, best first; equal entries stay in the order they were added
}

// NewMemory returns an empty memory backend.
func NewMemory() Store {
	return &memory{
		boards:             map[Key]*board{},
		titles:             map[string]int{},
		maxTitles:          maxTitles,
		maxBoardsPerTitle:  maxBoardsPerTitle,
		maxEntriesPerBoard: maxEntriesPerBoard,
	}
}

// better reports whether a ranks above c on this board: the score first, then
// the earlier At.
func (b *board) better(a, c *Entry) bool {
	if a.Score != c.Score {
		if b.ascending {
			return a.Score < c.Score
		}
		return a.Score > c.Score
	}
	return a.At.Before(c.At)
}

// insertAt is where e goes in order: after every entry that ranks at or above
// it, so an exact tie ranks below the one that was added first.
func (b *board) insertAt(e *Entry) int {
	return sort.Search(len(b.order), func(i int) bool { return b.better(e, b.order[i]) })
}

// indexOf is the position of e in order, or -1 if e is not on the board. The
// entries above e form a prefix of order, so the search lands at the start of
// e's group of equal entries; that group is scanned for e itself.
func (b *board) indexOf(e *Entry) int {
	i := sort.Search(len(b.order), func(i int) bool { return !b.better(b.order[i], e) })
	for ; i < len(b.order); i++ {
		if b.order[i] == e {
			return i
		}
	}
	return -1
}

// add puts e on the board as its player's entry. The caller has removed any
// previous entry for that player.
func (b *board) add(e *Entry) {
	i := b.insertAt(e)
	b.order = append(b.order, nil)
	copy(b.order[i+1:], b.order[i:])
	b.order[i] = e
	b.entries[e.OnlineID] = e
}

// remove takes e out of order; the caller replaces it in entries.
func (b *board) remove(e *Entry) {
	if i := b.indexOf(e); i >= 0 {
		b.order = append(b.order[:i], b.order[i+1:]...)
	}
}

// rankOf is the 1-based rank of the player's entry, 0 when they have none.
func (b *board) rankOf(onlineID string) uint32 {
	e := b.entries[onlineID]
	if e == nil {
		return 0
	}
	if i := b.indexOf(e); i >= 0 {
		return uint32(i + 1)
	}
	return 0
}

func (m *memory) Put(ctx context.Context, k Key, ascending bool, e Entry) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.boards[k]
	var old *Entry
	if b != nil {
		old = b.entries[e.OnlineID]
	}
	if old == nil {
		n := 0
		if b != nil {
			n = len(b.entries)
		}
		if n >= m.maxEntriesPerBoard {
			return 0, ErrFull
		}
		if b == nil && m.titles[k.Title] >= m.maxBoardsPerTitle {
			return 0, ErrFull
		}
		if b == nil && m.titles[k.Title] == 0 && len(m.titles) >= m.maxTitles {
			return 0, ErrFull // a new title, and the store already has the most it keeps
		}
	}
	if b == nil {
		b = &board{ascending: ascending, entries: map[string]*Entry{}}
		m.boards[k] = b
		m.titles[k.Title]++
	}
	e.Comment = append([]byte(nil), e.Comment...)
	if old == nil {
		b.add(&e)
	} else if b.better(&e, old) {
		b.remove(old)
		b.add(&e)
	}
	return b.rankOf(e.OnlineID), nil
}

func (m *memory) Range(ctx context.Context, k Key, offset, count int) (int, []Entry, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var order []*Entry
	if b := m.boards[k]; b != nil {
		order = b.order
	}
	total := len(order)
	lo := min(max(offset, 0), total)
	hi := min(lo+max(count, 0), total)
	rows := make([]Entry, 0, hi-lo)
	for _, e := range order[lo:hi] {
		rows = append(rows, *e)
	}
	return total, rows, nil
}

func (m *memory) Lookup(ctx context.Context, k Key, ids []string) (int, []Entry, []uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]Entry, len(ids))
	ranks := make([]uint32, len(ids))
	b := m.boards[k]
	if b == nil {
		return 0, rows, ranks, nil
	}
	for i, who := range ids {
		if e := b.entries[who]; e != nil {
			rows[i], ranks[i] = *e, b.rankOf(who)
		}
	}
	return len(b.order), rows, ranks, nil
}

func (m *memory) Boards(ctx context.Context) ([]Board, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Board, 0, len(m.boards))
	for k, b := range m.boards {
		bi := Board{Key: k, Entries: len(b.order)}
		if len(b.order) > 0 {
			bi.Leader = *b.order[0]
		}
		out = append(out, bi)
	}
	return out, nil
}

func (m *memory) Name() string { return "memory" }

func (m *memory) Close() error { return nil }
