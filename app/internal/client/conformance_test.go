package client

// Conformance: this package against the real server, built from ../server at
// the repository root and run on free loopback ports. Skips when the server
// cannot be built.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testTitle = "NPWR00000_00"

var serverBin, serverSkip string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "re-psn-conformance")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	serverBin, serverSkip = buildServer(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// buildServer builds the server from the repository root (three levels up from
// this package). It returns the binary, or why it could not.
func buildServer(dir string) (bin, skip string) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err.Error()
	}
	if _, err := os.Stat(filepath.Join(root, "server", "main.go")); err != nil {
		return "", "no server source at " + filepath.Join(root, "server")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		return "", "no go command on PATH"
	}
	bin = filepath.Join(dir, "re-psn")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command(gobin, "build", "-o", bin, "./server")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Sprintf("go build ./server: %v\n%s", err, out)
	}
	return bin, ""
}

// startServer runs the server on a free loopback port with its status page
// off and returns the address. It retries when another process takes the port first.
func startServer(t *testing.T, flags ...string) string {
	t.Helper()
	if serverSkip != "" {
		t.Skip("server not built: " + serverSkip)
	}
	for try := 0; try < 5; try++ {
		if addr, ok := tryStart(t, freePort(t), flags); ok {
			return addr
		}
	}
	t.Fatal("server did not start on any of 5 ports")
	return ""
}

// freePort finds a loopback port that is free for both TCP and UDP right now.
func freePort(t *testing.T) string {
	t.Helper()
	for {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		pc, err := net.ListenPacket("udp", addr)
		ln.Close()
		if err == nil {
			pc.Close()
			return addr
		}
	}
}

func tryStart(t *testing.T, addr string, flags []string) (string, bool) {
	var log bytes.Buffer
	cmd := exec.Command(serverBin, append([]string{"-addr", addr, "-http", ""}, flags...)...)
	cmd.Env = cleanEnv()
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if nc, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			nc.Close()
			t.Cleanup(func() {
				cmd.Process.Kill()
				<-exited
				if t.Failed() {
					t.Logf("server log:\n%s", log.String())
				}
			})
			return addr, true
		}
		select {
		case <-exited:
			t.Logf("server on %s exited:\n%s", addr, log.String())
			return "", false
		case <-time.After(20 * time.Millisecond):
		}
	}
	cmd.Process.Kill()
	<-exited
	t.Fatalf("server on %s did not answer within 10s:\n%s", addr, log.String())
	return "", false
}

// cleanEnv drops settings from the environment, so the server runs on its defaults.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "RE_PSN_") && !strings.HasPrefix(kv, "PSNR_") {
			env = append(env, kv)
		}
	}
	return env
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dial(t *testing.T, addr string) *Conn {
	t.Helper()
	c, err := Dial(testCtx(t), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func hello(t *testing.T, addr, name string, port uint16) (*Conn, HelloAck) {
	t.Helper()
	c := dial(t, addr)
	ack, err := c.Hello(testCtx(t), Hello{CommID: testTitle, OnlineID: name, P2PPort: port})
	if err != nil {
		t.Fatal(err)
	}
	return c, ack
}

// nextPush returns the next push, failing the test if none arrives within 5 seconds.
func nextPush(t *testing.T, c *Conn) Push {
	t.Helper()
	select {
	case p, ok := <-c.Events():
		if !ok {
			t.Fatal("events closed while waiting for a push")
		}
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no push within 5s")
	}
	return nil
}

// waitPush returns the next push of type T, skipping (and logging) others.
func waitPush[T Push](t *testing.T, c *Conn) T {
	t.Helper()
	for {
		p := nextPush(t, c)
		if v, ok := p.(T); ok {
			return v
		}
		t.Logf("skipped push %T %+v", p, p)
	}
}

func wantCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var se *ServerError
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("got error %v, want server error %d (%s)", err, code, code)
	}
}

func TestConformanceRoomsAndScores(t *testing.T) {
	addr := startServer(t)
	ctx := testCtx(t)
	alice, aAck := hello(t, addr, "alice", 40001)
	bob, bAck := hello(t, addr, "bob", 40002)

	loop := netip.MustParseAddr("127.0.0.1")
	if aAck.UserID == 0 || aAck.UserID == bAck.UserID || aAck.Token == 0 || aAck.PublicIP != loop {
		t.Fatalf("hello acks: %+v %+v", aAck, bAck)
	}
	if aAck.Relay() || !aAck.RoomCodes() {
		t.Fatalf("flags %#x: want room codes and no relay", aAck.Flags)
	}

	room, err := alice.CreateRoom(ctx, CreateRoom{Max: 4, External: []byte("ext"), MemberData: []byte("a-data")})
	if err != nil {
		t.Fatal(err)
	}
	if room.MyMember != 1 || room.Owner != 1 || len(room.Members) != 1 || !room.Members[0].Owner {
		t.Fatalf("created room: %+v", room)
	}

	list, err := bob.SearchRooms(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Rooms) != 1 {
		t.Fatalf("search: %+v", list)
	}
	if ri := list.Rooms[0]; ri.ID != room.ID || ri.Owner != "alice" || ri.Cur != 1 || ri.Max != 4 || string(ri.External) != "ext" {
		t.Fatalf("search entry: %+v", ri)
	}

	joined, err := bob.JoinRoom(ctx, room.ID, []byte("b-data"))
	if err != nil {
		t.Fatal(err)
	}
	if joined.MyMember != 2 || len(joined.Members) != 2 || joined.Members[0].OnlineID != "alice" {
		t.Fatalf("joined room: %+v", joined)
	}
	if got := joined.Members[0].Addr; got != netip.AddrPortFrom(loop, 40001) {
		t.Fatalf("alice's address as bob sees it: %v", got)
	}

	mj := waitPush[*MemberJoined](t, alice)
	if mj.Member.OnlineID != "bob" || mj.Member.ID != 2 || string(mj.Member.Data) != "b-data" ||
		mj.Member.Addr != netip.AddrPortFrom(loop, 40002) || mj.Member.UserID != bAck.UserID {
		t.Fatalf("member joined: %+v", mj)
	}
	if !room.Apply(mj) || len(room.Members) != 2 {
		t.Fatalf("room after join: %+v", room)
	}

	checkScores(t, alice, bob)

	if err := bob.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if err := bob.LeaveRoom(ctx, room.ID); err != nil {
		t.Fatal(err)
	}
	ml := waitPush[*MemberLeft](t, alice)
	if !room.Apply(ml) || ml.Member != 2 || room.Owner != 1 || len(room.Members) != 1 {
		t.Fatalf("member left %+v, room %+v", ml, room)
	}

	bob.Close()
	if _, err := bob.SearchRooms(ctx, 0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("request after Close: %v, want ErrClosed", err)
	}
}

func checkScores(t *testing.T, alice, bob *Conn) {
	t.Helper()
	ctx := testCtx(t)
	if rank, err := alice.RecordScore(ctx, Score{Board: 7, Value: 100, Comment: []byte("gg")}); err != nil || rank != 1 {
		t.Fatalf("alice's score: rank %d, %v", rank, err)
	}
	if rank, err := bob.RecordScore(ctx, Score{Board: 7, Value: 50}); err != nil || rank != 2 {
		t.Fatalf("bob's score: rank %d, %v", rank, err)
	}
	k, err := bob.Ranking(ctx, 7, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if k.Total != 2 || len(k.Rows) != 2 || k.Rows[0].OnlineID != "alice" || k.Rows[0].Score != 100 ||
		string(k.Rows[0].Comment) != "gg" || k.Rows[0].Recorded == 0 || k.Rows[1].Rank != 2 {
		t.Fatalf("ranking: %+v", k)
	}
	k, err = alice.RankingByID(ctx, 7, []string{"bob", "nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Rows) != 2 || k.Rows[0].Rank != 2 || k.Rows[0].Score != 50 || k.Rows[1].Rank != 0 || k.Rows[1].OnlineID != "nobody" {
		t.Fatalf("ranking by id: %+v", k)
	}
}

func TestConformanceRelayRoute(t *testing.T) {
	addr := startServer(t, "-relay")
	ctx := testCtx(t)
	alice, aAck := hello(t, addr, "alice", 0)
	bob, bAck := hello(t, addr, "bob", 0)
	if !aAck.Relay() {
		t.Fatalf("flags %#x: want the relay bit with -relay", aAck.Flags)
	}
	room, err := alice.CreateRoom(ctx, CreateRoom{Max: 2})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := bob.JoinRoom(ctx, room.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Alice gets MEMBER_JOINED, then both sides a ROUTE about the other. Neither has probed, so neither needs the
	// stream relay.
	if p := nextPush(t, alice); !room.Apply(p) || len(room.Members) != 2 {
		t.Fatalf("alice's first push: %T %+v", p, p)
	}
	aRoute := waitPush[*Route](t, alice)
	bRoute := waitPush[*Route](t, bob)
	if aRoute.Room != room.ID || aRoute.UserID != bAck.UserID || aRoute.StreamRelay() {
		t.Fatalf("alice's route: %+v", aRoute)
	}
	if bRoute.Room != room.ID || bRoute.UserID != aAck.UserID || bRoute.StreamRelay() {
		t.Fatalf("bob's route: %+v", bRoute)
	}
	if !joined.Apply(bRoute) || joined.Routes[aAck.UserID] != *bRoute {
		t.Fatalf("bob's room routes: %+v", joined.Routes)
	}
}

func TestConformanceErrors(t *testing.T) {
	addr := startServer(t)
	ctx := testCtx(t)

	t.Run("request before hello", func(t *testing.T) {
		_, err := dial(t, addr).SearchRooms(ctx, 0, 1)
		wantCode(t, err, ErrNoHello)
	})
	t.Run("truncated hello", func(t *testing.T) {
		_, err := dial(t, addr).call(ctx, "short hello", typeHello, typeHelloAck, func(e *encoder) { e.u8(1) })
		wantCode(t, err, ErrBadRequest)
	})

	c, _ := hello(t, addr, "carol", 0)
	t.Run("unknown type", func(t *testing.T) {
		_, err := c.call(ctx, "type 0x7F", 0x7F, typeOK, nil)
		wantCode(t, err, ErrBadRequest)
	})
	t.Run("join missing room", func(t *testing.T) {
		_, err := c.JoinRoom(ctx, 999, nil)
		wantCode(t, err, ErrNotFound)
	})
	t.Run("leave room not in", func(t *testing.T) {
		wantCode(t, c.LeaveRoom(ctx, 999), ErrNotInRoom)
	})
	t.Run("ranking from rank 0", func(t *testing.T) {
		_, err := c.call(ctx, "ranking from 0", typeGetRanking, typeRanking, func(e *encoder) {
			e.u32(1)
			e.u32(0)
			e.u16(1)
		})
		wantCode(t, err, ErrBadRequest)
	})
	t.Run("frame shorter than a request id", func(t *testing.T) {
		// Not in docs/api.md: the server drops such a frame without a reply and keeps the connection.
		c.writeLock <- struct{}{}
		err := writeFrame(c.nc, typeSearchRooms, []byte{0, 1})
		<-c.writeLock
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.SearchRooms(ctx, 0, 1); err != nil {
			t.Fatalf("request after the short frame: %v", err)
		}
	})
	t.Run("name taken from another address", func(t *testing.T) {
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)}, Timeout: 5 * time.Second}
		nc, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			t.Skip("cannot dial from 127.0.0.2: " + err.Error())
		}
		other := newConn(nc)
		defer other.Close()
		_, err = other.Hello(ctx, Hello{CommID: testTitle, OnlineID: "CAROL"})
		wantCode(t, err, ErrNameTaken)
	})
}

func TestHelloChecksFieldsBeforeSending(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := newConn(a)
	defer c.Close()
	for _, h := range []Hello{
		{CommID: "", OnlineID: "x"},
		{CommID: testTitle, OnlineID: ""},
		{CommID: testTitle + "X", OnlineID: "x"},
		{CommID: testTitle, OnlineID: strings.Repeat("x", 17)},
	} {
		if _, err := c.Hello(testCtx(t), h); err == nil || !strings.HasPrefix(err.Error(), "hello: ") {
			t.Errorf("Hello(%+v): %v, want a hello: error", h, err)
		}
	}
}

func TestDecodeTruncatedPush(t *testing.T) {
	for _, typ := range []byte{typeMemberJoined, typeMemberLeft, typeRoute} {
		if _, err := decodePush(typ, []byte{0, 0, 0}); !errors.Is(err, errTruncated) {
			t.Errorf("push 0x%02X with 3 bytes: %v, want an error", typ, err)
		}
	}
	if p, err := decodePush(0xA5, []byte{1, 2}); err != nil || p.(*RawPush).Type != 0xA5 {
		t.Errorf("undecoded push: %+v %v", p, err)
	}
}
