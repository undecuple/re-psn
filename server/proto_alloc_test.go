package main

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"testing"
)

func TestReadPacketRoundTripSizes(t *testing.T) {
	for _, n := range []int{0, 1, 2047, 2048, 2049, 4096, 4097, 30000, maxPayload} {
		want := make([]byte, n)
		for i := range want {
			want[i] = byte(i * 7)
		}
		var buf bytes.Buffer
		if err := writePacket(&buf, 0x42, want); err != nil {
			t.Fatal(err)
		}
		typ, got, err := readPacket(&buf)
		if err != nil || typ != 0x42 || !bytes.Equal(got, want) {
			t.Fatalf("payload of %d bytes: type %x, err %v, equal %v", n, typ, err, bytes.Equal(got, want))
		}
	}
}

func TestReadPacketTruncated(t *testing.T) {
	for _, have := range []int{0, 5, 2048, 5000} {
		b := append([]byte{0x01, 0xFF, 0xFF}, make([]byte, have)...)
		if _, _, err := readPacket(bytes.NewReader(b)); !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			t.Fatalf("%d of 65535 bytes: err %v, want an EOF error", have, err)
		}
	}
}

// A header that declares the largest payload and sends none must not cost the declared size.
func TestReadPacketDeclaredSizeNotAllocated(t *testing.T) {
	hdr := []byte{0x01, 0xFF, 0xFF}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 100; i++ {
		readPacket(bytes.NewReader(hdr))
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / 100; per > 8*1024 {
		t.Fatalf("a header declaring %d bytes with none sent allocated %d bytes", maxPayload, per)
	}
}
