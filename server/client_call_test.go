package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCClientCall checks psnr_call's reply handling against synthetic responders: an ERROR reply, a reply to another
// request id and a push ahead of ours, frames too short to carry a request id, a body that never completes, and the
// largest frame the wire allows, written in pieces. The truncated responder closes after 600 ms so the client's
// timeout is measured against a budget rather than a hang.
func TestCClientCall(t *testing.T) {
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(name); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_call.c not run")
	}
	exe := filepath.Join(t.TempDir(), "test_call")
	args := []string{"-std=gnu11", "-Wall", "-Wextra", "-Werror", "-I../client", "../client/psnr.c", "../client/test_call.c", "-o", exe}
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build call client: %v\n%s", err, out)
	}
	// reply frames a payload of req followed by body.
	reply := func(typ byte, req uint32, body []byte) []byte {
		f := make([]byte, 7, 7+len(body))
		f[0] = typ
		binary.BigEndian.PutUint16(f[1:], uint16(4+len(body)))
		binary.BigEndian.PutUint32(f[3:], req)
		return append(f, body...)
	}
	push := []byte{msgMemberLeft, 0, 10, 0, 0, 0, 0, 0, 0, 0, 7, 0, 2} // room u64 7 | member u16 2: a push carries no request id
	for _, mode := range []string{"error", "foreign", "short", "truncated", "max"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan struct{})
			defer close(done)
			serverErr := make(chan error, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					serverErr <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				hello := make([]byte, 37)
				if _, err = io.ReadFull(conn, hello); err == nil {
					ack := make([]byte, 20) // HELLO_ACK, req 1, user 1, loopback, token 1, relay on
					ack[0], ack[2], ack[6], ack[10], ack[11], ack[14], ack[18], ack[19] = msgHelloAck, 17, 1, 1, 127, 1, 1, 1
					_, err = conn.Write(ack)
				}
				header := make([]byte, 3)
				if err == nil {
					_, err = io.ReadFull(conn, header)
				}
				body := make([]byte, 4)
				if err == nil {
					if n := binary.BigEndian.Uint16(header[1:]); n != 4 || header[0] != msgGetCaps {
						t.Errorf("request 0x%02X with %d payload bytes, want GET_CAPS with 4", header[0], n)
					}
					_, err = io.ReadFull(conn, body)
				}
				if err != nil {
					serverErr <- err
					return
				}
				req := binary.BigEndian.Uint32(body)
				caps := reply(msgCaps, req, []byte{0, 0, 0, 1, 0, 0}) // bits 1, no version text
				write := func(parts ...[]byte) {
					for _, part := range parts {
						if err == nil {
							_, err = conn.Write(part)
						}
					}
				}
				pause := func(d time.Duration) bool {
					select {
					case <-time.After(d):
						return true
					case <-done:
						return false
					}
				}
				switch mode {
				case "error":
					write(reply(msgError, req, []byte{0, 0, 0, errPass}))
				case "foreign":
					write(reply(msgOK, req+1, nil), push, caps)
				case "short":
					write([]byte{msgCaps, 0, 0}, []byte{msgCaps, 0, 2, 0, 0}, caps)
				case "truncated":
					write(caps[:5]) // the 3-byte header (length 10) and 2 of the 4 request-id bytes; the rest never arrives
					pause(600 * time.Millisecond)
				case "max":
					data := make([]byte, 0xFFFF-4)
					for i := range data {
						data[i] = byte(i * 7)
					}
					big := reply(msgCaps, req, data)
					for off := 0; off < len(big) && err == nil; off += 4000 {
						end := off + 4000
						if end > len(big) {
							end = len(big)
						}
						write(big[off:end])
						if !pause(2 * time.Millisecond) {
							break
						}
					}
					write(push)
				}
				serverErr <- err
			}()
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, exe, port, mode).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "test_call: "+mode+" passed") {
				t.Fatalf("test_call %s: %v\n%s", mode, err, out)
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("call responder: %v", err)
			}
		})
	}
}
