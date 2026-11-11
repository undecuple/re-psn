package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

var limitBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func at(i int) time.Time { return limitBase.Add(time.Duration(i) * time.Second) }

func newLimited(boards, entries int) *memory {
	m := NewMemory().(*memory)
	m.maxBoardsPerTitle = boards
	m.maxEntriesPerBoard = entries
	return m
}

func TestMemoryDefaultLimits(t *testing.T) {
	m := NewMemory().(*memory)
	if m.maxBoardsPerTitle != 1000 || m.maxEntriesPerBoard != 10000 {
		t.Fatalf("limits %d and %d, want 1000 and 10000", m.maxBoardsPerTitle, m.maxEntriesPerBoard)
	}
}

func TestMemoryBoardLimit(t *testing.T) {
	ctx := context.Background()
	m := newLimited(2, 10)
	put := func(k Key, who string, score int64) (uint32, error) {
		return m.Put(ctx, k, false, Entry{OnlineID: who, Score: score, At: at(0)})
	}
	for b := uint32(1); b <= 2; b++ {
		if _, err := put(Key{"T", b}, "a", 1); err != nil {
			t.Fatalf("board %d: %v", b, err)
		}
	}
	if _, err := put(Key{"T", 3}, "a", 1); !errors.Is(err, ErrFull) {
		t.Fatalf("third board of a title: err %v, want ErrFull", err)
	}
	if boards, _ := m.Boards(ctx); len(boards) != 2 {
		t.Fatalf("%d boards after a refused one, want 2", len(boards))
	}
	if total, _, _ := m.Range(ctx, Key{"T", 3}, 0, 1); total != 0 {
		t.Fatalf("refused board has %d entries", total)
	}
	// the limit is per title
	if _, err := put(Key{"U", 3}, "a", 1); err != nil {
		t.Fatalf("other title's board: %v", err)
	}
	// boards that exist still take players and scores
	if _, err := put(Key{"T", 1}, "b", 5); err != nil {
		t.Fatalf("existing board: %v", err)
	}
}

func TestMemoryEntryLimit(t *testing.T) {
	ctx := context.Background()
	m := newLimited(10, 3)
	k := Key{"T", 1}
	put := func(who string, score int64, i int) (uint32, error) {
		return m.Put(ctx, k, false, Entry{OnlineID: who, Score: score, Comment: []byte(who), At: at(i)})
	}
	for i, p := range []struct {
		who   string
		score int64
	}{{"a", 100}, {"b", 200}, {"c", 150}} {
		if _, err := put(p.who, p.score, i); err != nil {
			t.Fatalf("%s: %v", p.who, err)
		}
	}
	if _, err := put("d", 999, 3); !errors.Is(err, ErrFull) {
		t.Fatalf("fourth player: err %v, want ErrFull", err)
	}
	if _, err := put("d", 999, 3); !errors.Is(err, ErrFull) {
		t.Fatalf("fourth player again: err %v, want ErrFull", err)
	}
	total, rows, _ := m.Range(ctx, k, 0, 10)
	if total != 3 || len(rows) != 3 || rows[0].OnlineID != "b" || rows[2].OnlineID != "a" {
		t.Fatalf("after a refused player: total %d rows %+v", total, rows)
	}
	if _, _, ranks, _ := m.Lookup(ctx, k, []string{"d"}); ranks[0] != 0 {
		t.Fatalf("refused player has rank %d", ranks[0])
	}
	// a player already on the full board: a worse score is kept out, a better one replaces
	if rank, err := put("a", 50, 4); err != nil || rank != 3 {
		t.Fatalf("worse score from a player on a full board: rank %d err %v, want 3 and nil", rank, err)
	}
	if rank, err := put("a", 500, 5); err != nil || rank != 1 {
		t.Fatalf("better score from a player on a full board: rank %d err %v, want 1 and nil", rank, err)
	}
	total, rows, _ = m.Range(ctx, k, 0, 10)
	if total != 3 || rows[0].OnlineID != "a" || rows[0].Score != 500 || string(rows[0].Comment) != "a" {
		t.Fatalf("after an update at the limit: total %d rows %+v", total, rows)
	}
	// another board of the same title has its own limit
	if _, err := m.Put(ctx, Key{"T", 2}, false, Entry{OnlineID: "d", Score: 1, At: at(6)}); err != nil {
		t.Fatalf("player on another board: %v", err)
	}
}

// TestMemoryRankMatchesSort checks the rank and order against a plain sort of
// each player's best score: higher (or lower) first, ties to the earlier At.
// Scores repeat a lot, so most ranks are decided by the tie rule.
func TestMemoryRankMatchesSort(t *testing.T) {
	ctx := context.Background()
	for _, asc := range []bool{false, true} {
		rng := rand.New(rand.NewSource(1))
		m := newLimited(10, 1000)
		k := Key{"T", 1}
		best := map[string]Entry{}
		check := func(step int) {
			want := make([]Entry, 0, len(best))
			for _, e := range best {
				want = append(want, e)
			}
			sort.Slice(want, func(i, j int) bool {
				a, c := want[i], want[j]
				if a.Score != c.Score {
					return (a.Score > c.Score) != asc
				}
				return a.At.Before(c.At)
			})
			total, rows, _ := m.Range(ctx, k, 0, len(want)+1)
			if total != len(want) || len(rows) != len(want) {
				t.Fatalf("asc %v step %d: %d rows, want %d", asc, step, len(rows), len(want))
			}
			ids := make([]string, len(want))
			for i, w := range want {
				if rows[i].OnlineID != w.OnlineID {
					t.Fatalf("asc %v step %d: row %d is %s, want %s", asc, step, i, rows[i].OnlineID, w.OnlineID)
				}
				ids[i] = w.OnlineID
			}
			_, _, ranks, _ := m.Lookup(ctx, k, ids)
			for i := range ids {
				if ranks[i] != uint32(i+1) {
					t.Fatalf("asc %v step %d: %s has rank %d, want %d", asc, step, ids[i], ranks[i], i+1)
				}
			}
		}
		for i := 0; i < 1500; i++ {
			who := fmt.Sprintf("p%d", rng.Intn(120))
			e := Entry{OnlineID: who, Score: int64(rng.Intn(15)), At: at(0).Add(time.Duration(i) * time.Millisecond)}
			if _, err := m.Put(ctx, k, asc, e); err != nil {
				t.Fatal(err)
			}
			if old, ok := best[who]; !ok || (e.Score != old.Score && (e.Score > old.Score) != asc) {
				best[who] = e
			}
			if i%25 == 0 {
				check(i)
			}
		}
		check(-1)
	}
}
