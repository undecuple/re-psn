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

// TestCClientStreamDeadline checks both relay entry points against fragmented and
// stalled synthetic replies, and checks the returned socket still carries raw,
// blocking traffic. The stalled-body responder closes after 600 ms so the old
// code fails the budget assertion without hanging the test process.
func TestCClientStreamDeadline(t *testing.T) {
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(name); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_stream.c not run")
	}
	exe := filepath.Join(t.TempDir(), "test_stream")
	args := []string{"-std=gnu11", "-Wall", "-Wextra", "-I../client", "../client/psnr.c", "../client/test_stream.c", "-o", exe}
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build stream client: %v\n%s", err, out)
	}
	for _, operation := range []string{"connect", "accept"} {
		for _, mode := range []string{"fragmented-header", "stalled-body", "success", "negative-timeout", "refused"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ln, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
				done := make(chan struct{})
				defer close(done)
				started := make(chan time.Time, 1)
				serverErr := make(chan error, 1)
				go func() {
					control, err := ln.Accept()
					if err != nil {
						serverErr <- err
						return
					}
					defer control.Close()
					control.SetDeadline(time.Now().Add(3 * time.Second))
					hello := make([]byte, 37)
					if _, err = io.ReadFull(control, hello); err == nil {
						ack := make([]byte, 20) // HELLO_ACK, req 1, user 1, loopback, token 1, relay on
						ack[0], ack[2], ack[6], ack[10], ack[11], ack[14], ack[18], ack[19] = msgHelloAck, 17, 1, 1, 127, 1, 1, 1
						_, err = control.Write(ack)
					}
					if err != nil {
						serverErr <- err
						return
					}
					stream, err := ln.Accept()
					if err != nil {
						serverErr <- err
						return
					}
					defer stream.Close()
					stream.SetDeadline(time.Now().Add(3 * time.Second))
					header := make([]byte, 3)
					if _, err = io.ReadFull(stream, header); err == nil {
						body := make([]byte, binary.BigEndian.Uint16(header[1:]))
						_, err = io.ReadFull(stream, body)
					}
					if err != nil {
						serverErr <- err
						return
					}
					started <- time.Now()
					pause := func(d time.Duration) bool {
						select {
						case <-time.After(d):
							return true
						case <-done:
							return false
						}
					}
					ready := []byte{msgStreamReady, 0, 4, 0, 0, 0, 1}
					switch mode {
					case "fragmented-header":
						for _, part := range [][]byte{ready[:1], ready[1:2], ready[2:]} {
							if _, err = stream.Write(part); err != nil || !pause(80*time.Millisecond) {
								return
							}
						}
					case "stalled-body":
						stream.Write(ready[:4])
						pause(600 * time.Millisecond)
					case "refused":
						_, err = stream.Write([]byte{msgError, 0, 8, 0, 0, 0, 1, 0, 0, 0, 9})
					case "success", "negative-timeout":
						_, err = stream.Write(ready)
						if err == nil && pause(30*time.Millisecond) {
							_, err = stream.Write([]byte{'x'})
							if err == nil {
								_, err = io.ReadFull(stream, ready[:1])
								if err == nil && ready[0] != 'y' {
									t.Error("stream did not carry the client's raw byte")
								}
							}
						}
					}
					serverErr <- err
				}()
				_, port, _ := net.SplitHostPort(ln.Addr().String())
				want := "failure"
				timeout := "120"
				if mode == "success" || mode == "negative-timeout" {
					want = "success"
				}
				if mode == "negative-timeout" {
					timeout = "-1"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, exe, port, operation, timeout, want).CombinedOutput()
				finished := time.Now()
				if err != nil || !strings.Contains(string(out), "result="+want) {
					t.Fatalf("stream client: %v\n%s", err, out)
				}
				if mode == "fragmented-header" || mode == "stalled-body" {
					select {
					case start := <-started:
						if elapsed := finished.Sub(start); elapsed > 400*time.Millisecond {
							t.Fatalf("120 ms stream setup exceeded its budget: %v", elapsed)
						}
					default:
						t.Fatal("stream request did not reach responder")
					}
				} else if err := <-serverErr; err != nil {
					t.Fatalf("stream responder: %v", err)
				}
			})
		}
	}
}
