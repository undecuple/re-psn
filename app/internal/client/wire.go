package client

// Framing and field codecs for the protocol in docs/api.md. Every integer is
// big-endian; a frame is u8 type | u16 length | payload.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Message types this package sends or reads (docs/api.md).
const (
	typeHello        = 0x01
	typeHeartbeat    = 0x02
	typeAuth         = 0x05
	typeCreateRoom   = 0x10
	typeSearchRooms  = 0x11
	typeJoinRoom     = 0x12
	typeLeaveRoom    = 0x13
	typeRecordScore  = 0x20
	typeGetRanking   = 0x21
	typeGetRankingID = 0x22

	typeHelloAck      = 0x81
	typeError         = 0x8F
	typeRoomJoined    = 0x90
	typeRoomList      = 0x91
	typeOK            = 0x93
	typeScoreRecorded = 0xB0
	typeRanking       = 0xB1

	typeMemberJoined = 0xA1
	typeMemberLeft   = 0xA2
	typeRoute        = 0xA7

	pushFirst = 0xA0 // pushes are 0xA0-0xAF and carry no request id
	pushLast  = 0xAF
)

// Field widths and limits.
const (
	commIDLen   = 12
	onlineIDLen = 16
	maxPayload  = 0xFFFF
	maxPass     = 2048 // AUTH pass blob
	maxRoomBlob = 4096 // every blob put in a room
	maxComment  = 64   // RECORD_SCORE comment
	maxRows     = 100  // GET_RANKING count, GET_RANKING_BY_ID n
)

func isPush(t byte) bool { return t >= pushFirst && t <= pushLast }

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	p := make([]byte, binary.BigEndian.Uint16(hdr[1:]))
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, fmt.Errorf("frame type 0x%02X: payload: %w", hdr[0], err)
	}
	return hdr[0], p, nil
}

func writeFrame(w io.Writer, t byte, p []byte) error {
	if len(p) > maxPayload {
		return fmt.Errorf("frame type 0x%02X: payload of %d bytes is over %d", t, len(p), maxPayload)
	}
	buf := make([]byte, 3, 3+len(p))
	buf[0] = t
	binary.BigEndian.PutUint16(buf[1:], uint16(len(p)))
	_, err := w.Write(append(buf, p...))
	return err
}

var errTruncated = errors.New("truncated payload")

// decoder walks a payload; the first short read latches err, so callers read
// every field and check once.
type decoder struct {
	b   []byte
	err error
}

func (d *decoder) take(n int) []byte {
	if d.err != nil || len(d.b) < n {
		d.err = errTruncated
		return make([]byte, n)
	}
	v := d.b[:n]
	d.b = d.b[n:]
	return v
}

func (d *decoder) u8() uint8   { return d.take(1)[0] }
func (d *decoder) u16() uint16 { return binary.BigEndian.Uint16(d.take(2)) }
func (d *decoder) u32() uint32 { return binary.BigEndian.Uint32(d.take(4)) }
func (d *decoder) u64() uint64 { return binary.BigEndian.Uint64(d.take(8)) }
func (d *decoder) ip() [4]byte { return [4]byte(d.take(4)) }

func (d *decoder) blob() []byte { return append([]byte(nil), d.take(int(d.u16()))...) }

// str reads a fixed-width NUL-padded field.
func (d *decoder) str(n int) string {
	b := d.take(n)
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

type encoder struct{ b []byte }

func (e *encoder) u8(v uint8)   { e.b = append(e.b, v) }
func (e *encoder) u16(v uint16) { e.b = binary.BigEndian.AppendUint16(e.b, v) }
func (e *encoder) u32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *encoder) u64(v uint64) { e.b = binary.BigEndian.AppendUint64(e.b, v) }
func (e *encoder) blob(v []byte) {
	e.u16(uint16(len(v)))
	e.b = append(e.b, v...)
}

// str writes s into a fixed-width NUL-padded field; callers check the length first.
func (e *encoder) str(s string, n int) {
	f := make([]byte, n)
	copy(f, s)
	e.b = append(e.b, f...)
}
