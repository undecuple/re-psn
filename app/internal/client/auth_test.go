package client

// AUTH against the real server with passes signed by throwaway Ed25519 keys made here. Nothing is a real key.

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	authIssuer = "login.test"
	authAud    = "server.test"
)

var authB64 = base64.RawURLEncoding

type authKeys struct {
	t    *testing.T
	priv ed25519.PrivateKey
	file string
}

func newAuthKeys(t *testing.T) *authKeys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(file, []byte("k1 "+authB64.EncodeToString(pub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &authKeys{t, priv, file}
}

// pass signs a payload for sub with the given scopes, valid for an hour from now.
func (k *authKeys) pass(sub string, scope ...string) []byte {
	now := time.Now()
	b, err := json.Marshal(map[string]any{
		"v": "v1", "kid": "k1", "iss": authIssuer, "aud": authAud, "sub": sub,
		"scope": scope, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		k.t.Fatal(err)
	}
	head := "v1." + authB64.EncodeToString(b)
	return []byte(head + "." + authB64.EncodeToString(ed25519.Sign(k.priv, []byte(head))))
}

func (k *authKeys) server(t *testing.T, require string) string {
	t.Helper()
	return startServer(t, "-relay", "-pass-key-file", k.file, "-pass-issuer", authIssuer, "-pass-audience", authAud,
		"-require-pass", require, "-pass-max-unauth-per-addr", "0")
}

func TestAuthConnectMode(t *testing.T) {
	k := newAuthKeys(t)
	addr := k.server(t, "connect")
	ctx := testCtx(t)
	pass := k.pass("github:1", "connect", "relay")

	c, ack := hello(t, addr, "alice", 0)
	if !ack.PassRequired() || ack.Token != 0 {
		t.Fatalf("HELLO_ACK flags 0x%02X token %d: want the pass flag and token 0", ack.Flags, ack.Token)
	}
	// Before AUTH the server answers a room request with ERROR 10.
	_, err := c.SearchRooms(ctx, 0, 1)
	wantCode(t, err, ErrPass)

	r, err := c.Auth(ctx, pass)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasToken || r.Token == 0 {
		t.Fatalf("AUTH reply %+v: want a token", r)
	}
	if _, err := c.SearchRooms(ctx, 0, 1); err != nil {
		t.Fatalf("request after AUTH: %v", err)
	}

	// A refresh with a new pass for the same account returns the same token.
	r2, err := c.Auth(ctx, k.pass("github:1", "connect"))
	if err != nil || r2 != r {
		t.Fatalf("refresh: %+v, %v; want %+v", r2, err, r)
	}
	// A refresh for another account is refused and changes nothing.
	_, err = c.Auth(ctx, k.pass("github:2", "connect"))
	wantCode(t, err, ErrPass)
	if _, err := c.SearchRooms(ctx, 0, 1); err != nil {
		t.Fatalf("request after a refused refresh: %v", err)
	}
}

func TestAuthRefusals(t *testing.T) {
	k := newAuthKeys(t)
	addr := k.server(t, "connect")
	ctx := testCtx(t)
	other := newAuthKeys(t)

	t.Run("bad signature", func(t *testing.T) {
		c, _ := hello(t, addr, "bob", 0)
		pass := other.pass("github:3", "connect")
		_, err := c.Auth(ctx, pass)
		wantCode(t, err, ErrPass)
		if strings.Contains(err.Error(), string(pass)) {
			t.Fatal("the error text holds the pass")
		}
	})
	t.Run("missing connect scope", func(t *testing.T) {
		c, _ := hello(t, addr, "bob", 0)
		_, err := c.Auth(ctx, k.pass("github:3", "relay"))
		wantCode(t, err, ErrPass)
	})
	t.Run("name taken", func(t *testing.T) {
		c, _ := hello(t, addr, "carol", 0)
		if _, err := c.Auth(ctx, k.pass("github:4", "connect")); err != nil {
			t.Fatal(err)
		}
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)}, Timeout: 5 * time.Second}
		nc, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			t.Skip("cannot dial from 127.0.0.2: " + err.Error())
		}
		o := newConn(nc)
		defer o.Close()
		if _, err := o.Hello(ctx, Hello{CommID: testTitle, OnlineID: "carol"}); err != nil {
			t.Fatal(err)
		}
		_, err = o.Auth(ctx, k.pass("github:5", "connect"))
		wantCode(t, err, ErrNameTaken)
	})
	t.Run("empty and oversize are refused before sending", func(t *testing.T) {
		c, _ := hello(t, addr, "dave", 0)
		for _, n := range []int{0, 2049} {
			if _, err := c.Auth(ctx, make([]byte, n)); err == nil {
				t.Fatalf("a pass of %d bytes was sent", n)
			}
		}
		// Nothing was sent, so the connection still works: a good pass is accepted and the count of refusals is 0.
		if _, err := c.Auth(ctx, k.pass("github:6", "connect")); err != nil {
			t.Fatalf("AUTH after the refused-locally calls: %v", err)
		}
	})
	t.Run("2048 bytes is sent", func(t *testing.T) {
		c, _ := hello(t, addr, "erin", 0)
		_, err := c.Auth(ctx, []byte(strings.Repeat("x", 2048)))
		wantCode(t, err, ErrPass)
	})
}

func TestAuthRelayOnlyMode(t *testing.T) {
	k := newAuthKeys(t)
	addr := k.server(t, "relay")
	c, ack := hello(t, addr, "alice", 0)
	if ack.PassRequired() || ack.Token == 0 {
		t.Fatalf("HELLO_ACK flags 0x%02X token %d: want no pass flag and a token", ack.Flags, ack.Token)
	}
	r, err := c.Auth(testCtx(t), k.pass("github:1", "relay"))
	if err != nil {
		t.Fatal(err)
	}
	if r.HasToken {
		t.Fatalf("relay-only AUTH reply carries a token: %+v", r)
	}
}

func TestAuthOldServerAnswersBadRequest(t *testing.T) {
	addr := startServer(t) // no pass settings: the same answer a server from before AUTH gives
	c, ack := hello(t, addr, "alice", 0)
	if ack.PassRequired() {
		t.Fatal("pass flag set on a server with no pass settings")
	}
	_, err := c.Auth(testCtx(t), []byte("v1.a.b"))
	wantCode(t, err, ErrBadRequest)
}

func TestAuthTokenOptional(t *testing.T) {
	// The reply's token is read only when exactly four bytes are there.
	for _, tc := range []struct {
		body []byte
		want AuthReply
	}{
		{nil, AuthReply{}},
		{[]byte{0, 0, 1, 2}, AuthReply{Token: 258, HasToken: true}},
		{[]byte{0, 0, 1}, AuthReply{}},
		{[]byte{0, 0, 1, 2, 3, 4, 5, 6, 7}, AuthReply{}},
	} {
		d := &decoder{b: tc.body}
		if r := decodeAuthReply(d); r != tc.want {
			t.Errorf("%v: %+v, want %+v", tc.body, r, tc.want)
		}
	}
}
