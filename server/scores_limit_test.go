package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sp00nznet/psnr/server/store"
)

// full is a score store that refuses every new Put as a full store does.
type full struct{ store.Store }

func (full) Put(context.Context, store.Key, bool, store.Entry) (uint32, error) {
	return 0, store.ErrFull
}

// TestBoardLimitAnswersSix: a Put the store refuses for a limit gets ERROR 6,
// logged at debug level and not as a failure.
func TestBoardLimitAnswersSix(t *testing.T) {
	s, addr, buf := logged(t)
	s.scores = full{store.NewMemory()}
	a := dial(t, addr, "NPWR00002_00", "alice", 0)
	rec := &writer{}
	rec.u32(1)
	rec.u8(0)
	rec.u64(100)
	rec.blob(nil)
	if rt, r := a.call(msgRecordScore, rec.b); rt != msgError || r.u32() != errStoreFailed {
		t.Fatalf("record at the limit: got 0x%02X, want ERROR 6", rt)
	}
	time.Sleep(50 * time.Millisecond)
	out := buf.String()
	if !strings.Contains(out, `level=DEBUG msg="score refused"`) {
		t.Errorf("no debug line for the refused score:\n%s", out)
	}
	if strings.Contains(out, `msg="request failed"`) {
		t.Errorf("a limit was logged as a failed request:\n%s", out)
	}
}

// TestRankingByIDRefusesBeforeReading: a count over 100 is refused before any
// id is read, so a request that claims 65535 ids is answered with ERROR 1
// and the ids are left unread.
func TestRankingByIDRefusesBeforeReading(t *testing.T) {
	s := NewServer(10, false)
	const n = 65535
	w := &writer{}
	w.u32(7)                                 // board
	w.u16(n)                                 // count
	w.b = append(w.b, make([]byte, 16*n)...) // all the ids, present
	rd := &reader{b: w.b}
	rt, body := s.getRankingByID(&client{}, scorer{commID: "NPWR00002_00", onlineID: "alice"}, 9, rd)
	r := &reader{b: body}
	if rt != msgError || r.u32() != 9 || r.u32() != errBadRequest {
		t.Fatalf("got 0x%02X: want ERROR 1 for request 9", rt)
	}
	if left := len(rd.b); left != 16*n {
		t.Fatalf("%d bytes left of the ids, want all %d: the ids were read", left, 16*n)
	}
}

// TestRankingByIDLargeCountWire: over the wire, a count of 65535 with no ids
// after it gets ERROR 1.
func TestRankingByIDLargeCountWire(t *testing.T) {
	_, addr, _ := logged(t)
	a := dial(t, addr, "NPWR00002_00", "alice", 0)
	head := &writer{}
	head.u32(1)
	head.u16(65535)
	if rt, r := a.call(msgGetRankingID, head.b); rt != msgError || r.u32() != errBadRequest {
		t.Fatalf("count 65535, no ids: got 0x%02X, want ERROR 1", rt)
	}
}
