package main

// NP Score-style leaderboards: one board per (title, board id), each user's
// best score kept, ties broken by who got there first (as NP Score ranks).
// The sort order comes from the first RECORD_SCORE for a board. On PSN it
// is server-side board config the title never sends; here the client passes
// the order it knows from the title.
//
// The boards live in s.scores (server/store), which has its own lock: these
// handlers run outside s.mu, so a slow backend never holds up rooms. A store
// error answers the request with ERROR 6 and is logged.

import (
	"context"
	"errors"
	"time"

	"github.com/sp00nznet/psnr/server/store"
)

// storeTimeout bounds every call to the score store.
const storeTimeout = 2 * time.Second

// errStoreFailed is the code a request gets when the score store fails. The
// protocol has no code for a server-side failure; 6 (limit reached) is the
// closest: the request was fine, the server could not take it now.
const errStoreFailed = errLimitReached

// scorer is who a score request is from, read under s.mu.
type scorer struct {
	commID, onlineID string
}

func storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), storeTimeout)
}

// scoreRequest answers RECORD_SCORE, GET_RANKING and GET_RANKING_BY_ID.
func (s *Server) scoreRequest(c *client, who scorer, t byte, req uint32, rd *reader) (byte, []byte) {
	switch t {
	case msgRecordScore:
		return s.recordScore(c, who, req, rd)
	case msgGetRanking:
		return s.getRanking(c, who, req, rd)
	}
	return s.getRankingByID(c, who, req, rd)
}

func (s *Server) storeFailed(c *client, who scorer, what string, req uint32, err error) (byte, []byte) {
	s.log.Warn("request failed", "client", c.id, "online_id", who.onlineID, "request", what, "reason", "score store", "err", err)
	return msgError, errReply(req, errStoreFailed)
}

// RECORD_SCORE: board u32 | order u8 (0 higher-is-better, 1 lower) | score s64 | comment blob
// -> SCORE_RECORDED: req | rank u32 (the user's rank after recording; their
// best stands if this one is worse)
func (s *Server) recordScore(c *client, who scorer, req uint32, rd *reader) (byte, []byte) {
	id, order, score, comment := rd.u32(), rd.u8(), int64(rd.u64()), rd.blob()
	if rd.err != nil || len(comment) > 64 {
		return msgError, errReply(req, errBadRequest)
	}
	ctx, cancel := storeCtx()
	defer cancel()
	e := store.Entry{OnlineID: who.onlineID, Score: score, Comment: comment, At: time.Now()}
	rank, err := s.scores.Put(ctx, store.Key{Title: who.commID, Board: id}, order == 1, e)
	if errors.Is(err, store.ErrFull) {
		// a limit, not a failure: the player can keep scoring on boards that have room
		s.log.Debug("score refused", "client", c.id, "online_id", who.onlineID, "reason", "board limit", "err", err)
		return msgError, errReply(req, errStoreFailed)
	}
	if err != nil {
		return s.storeFailed(c, who, "record score", req, err)
	}
	w := &writer{}
	w.u32(req)
	w.u32(rank)
	return msgScoreRecorded, w.b
}

// RANKING: req | total u32 | count u16 | entries...
// entry: rank u32 | online_id [16] | score s64 | comment blob | recorded unix u64
// rank 0 means "no score" (only from GET_RANKING_BY_ID).
func rankingReply(req uint32, total int, rows []store.Entry, ranks []uint32, ids []string) []byte {
	w := &writer{}
	w.u32(req)
	w.u32(uint32(total))
	w.u16(uint16(len(rows)))
	for i, e := range rows {
		w.u32(ranks[i])
		if ranks[i] == 0 {
			w.str(ids[i], 16)
			w.u64(0)
			w.blob(nil)
			w.u64(0)
			continue
		}
		w.str(e.OnlineID, 16)
		w.u64(uint64(e.Score))
		w.blob(e.Comment)
		w.u64(uint64(e.At.Unix()))
	}
	return w.b
}

// GET_RANKING: board u32 | start u32 (1-based rank) | count u16
func (s *Server) getRanking(c *client, who scorer, req uint32, rd *reader) (byte, []byte) {
	id, start, count := rd.u32(), rd.u32(), rd.u16()
	if rd.err != nil || start == 0 || count > 100 {
		return msgError, errReply(req, errBadRequest)
	}
	ctx, cancel := storeCtx()
	defer cancel()
	total, rows, err := s.scores.Range(ctx, store.Key{Title: who.commID, Board: id}, int(start-1), int(count))
	if err != nil {
		return s.storeFailed(c, who, "get ranking", req, err)
	}
	ranks := make([]uint32, len(rows))
	for i := range rows {
		ranks[i] = start + uint32(i)
	}
	return msgRanking, rankingReply(req, total, rows, ranks, nil)
}

// GET_RANKING_BY_ID: board u32 | n u16 | online_id [16] * n (friends' ranks)
func (s *Server) getRankingByID(c *client, who scorer, req uint32, rd *reader) (byte, []byte) {
	id, n := rd.u32(), rd.u16()
	if rd.err != nil || n > 100 {
		return msgError, errReply(req, errBadRequest)
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = rd.str(16)
	}
	if rd.err != nil {
		return msgError, errReply(req, errBadRequest)
	}
	ctx, cancel := storeCtx()
	defer cancel()
	total, rows, ranks, err := s.scores.Lookup(ctx, store.Key{Title: who.commID, Board: id}, ids)
	if err != nil {
		return s.storeFailed(c, who, "get ranking by id", req, err)
	}
	return msgRanking, rankingReply(req, total, rows, ranks, ids)
}
