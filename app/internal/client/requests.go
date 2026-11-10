package client

// One method per request this package implements (docs/api.md).

import (
	"context"
	"fmt"
	"time"
)

// Hello sends HELLO, which must be the first request on a connection.
func (c *Conn) Hello(ctx context.Context, h Hello) (HelloAck, error) {
	const op = "hello"
	if err := h.validate(); err != nil {
		return HelloAck{}, fmt.Errorf("%s: %w", op, err)
	}
	d, err := c.call(ctx, op, typeHello, typeHelloAck, h.encode)
	if err != nil {
		return HelloAck{}, err
	}
	return finish(op, d, decodeHelloAck(d))
}

// Heartbeat sends HEARTBEAT, which keeps the connection alive and has no reply. After ReportRTT(true), and once a
// call has been timed, it carries the round trip in milliseconds (rtt_ms); otherwise it has no body, as before.
func (c *Conn) Heartbeat(ctx context.Context) error {
	var body func(*encoder)
	if c.reportRTT.Load() {
		if ms, ok := c.rtt.min(); ok {
			body = func(e *encoder) { e.u16(ms) }
		}
	}
	if err := c.send(ctx, typeHeartbeat, c.nextReq.Add(1), body); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	return nil
}

// KeepAlive sends a HEARTBEAT every interval (HeartbeatEvery if 0) until ctx
// ends or the connection does. It returns why it stopped.
func (c *Conn) KeepAlive(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = HeartbeatEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return c.endErr()
		case <-t.C:
			if err := c.Heartbeat(ctx); err != nil {
				return err
			}
		}
	}
}

// CreateRoom creates a room of this player's title; the caller becomes its owner.
func (c *Conn) CreateRoom(ctx context.Context, r CreateRoom) (Room, error) {
	const op = "create room"
	if err := r.validate(); err != nil {
		return Room{}, fmt.Errorf("%s: %w", op, err)
	}
	d, err := c.call(ctx, op, typeCreateRoom, typeRoomJoined, r.encode)
	if err != nil {
		return Room{}, err
	}
	return finish(op, d, decodeRoom(d))
}

// SearchRooms lists the open rooms of this player's title, oldest first, from offset start.
func (c *Conn) SearchRooms(ctx context.Context, start, max uint16) (RoomList, error) {
	const op = "search rooms"
	d, err := c.call(ctx, op, typeSearchRooms, typeRoomList, func(e *encoder) {
		e.u16(start)
		e.u16(max)
	})
	if err != nil {
		return RoomList{}, err
	}
	return finish(op, d, decodeRoomList(d))
}

// JoinRoom joins a room by id with this player's member data.
func (c *Conn) JoinRoom(ctx context.Context, room uint64, memberData []byte) (Room, error) {
	op := fmt.Sprintf("join room %d", room)
	if err := blobsFit(memberData); err != nil {
		return Room{}, fmt.Errorf("%s: %w", op, err)
	}
	d, err := c.call(ctx, op, typeJoinRoom, typeRoomJoined, func(e *encoder) {
		e.u64(room)
		e.blob(memberData)
	})
	if err != nil {
		return Room{}, err
	}
	return finish(op, d, decodeRoom(d))
}

// LeaveRoom leaves a room.
func (c *Conn) LeaveRoom(ctx context.Context, room uint64) error {
	op := fmt.Sprintf("leave room %d", room)
	_, err := c.call(ctx, op, typeLeaveRoom, typeOK, func(e *encoder) { e.u64(room) })
	return err
}

// RecordScore submits a score and returns the player's rank on the board
// afterwards (their best stands if this one is worse).
func (c *Conn) RecordScore(ctx context.Context, s Score) (uint32, error) {
	op := fmt.Sprintf("record score on board %d", s.Board)
	if len(s.Comment) > maxComment {
		return 0, fmt.Errorf("%s: comment of %d bytes is over %d", op, len(s.Comment), maxComment)
	}
	d, err := c.call(ctx, op, typeRecordScore, typeScoreRecorded, s.encode)
	if err != nil {
		return 0, err
	}
	return finish(op, d, d.u32())
}

// Ranking reads count rows (at most 100) of a board from rank start (1-based).
func (c *Conn) Ranking(ctx context.Context, board, start uint32, count uint16) (Ranking, error) {
	op := fmt.Sprintf("get ranking of board %d", board)
	if start == 0 || count > maxRows {
		return Ranking{}, fmt.Errorf("%s: want start >= 1 and count <= %d", op, maxRows)
	}
	d, err := c.call(ctx, op, typeGetRanking, typeRanking, func(e *encoder) {
		e.u32(board)
		e.u32(start)
		e.u16(count)
	})
	if err != nil {
		return Ranking{}, err
	}
	return finish(op, d, decodeRanking(d))
}

// RankingByID reads one row per online ID (at most 100), in the order given.
func (c *Conn) RankingByID(ctx context.Context, board uint32, ids []string) (Ranking, error) {
	op := fmt.Sprintf("get ranking by id of board %d", board)
	if len(ids) > maxRows {
		return Ranking{}, fmt.Errorf("%s: %d ids, at most %d", op, len(ids), maxRows)
	}
	for _, id := range ids {
		if len(id) > onlineIDLen {
			return Ranking{}, fmt.Errorf("%s: online id %q is over %d bytes", op, id, onlineIDLen)
		}
	}
	d, err := c.call(ctx, op, typeGetRankingID, typeRanking, func(e *encoder) {
		e.u32(board)
		e.u16(uint16(len(ids)))
		for _, id := range ids {
			e.str(id, onlineIDLen)
		}
	})
	if err != nil {
		return Ranking{}, err
	}
	return finish(op, d, decodeRanking(d))
}
