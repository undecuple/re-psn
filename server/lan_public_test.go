package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func lanPublicServer() *Server {
	s := NewServer(10, false)
	s.lanPublic = [4]byte{203, 0, 113, 50}
	return s
}

// lanMember is a player who connected over the LAN: its TCP and probe
// addresses are private.
func lanMember() *client {
	return &client{ip: [4]byte{192, 168, 1, 20}, p2pPort: 3660,
		udpIP: [4]byte{192, 168, 1, 20}, udpPort: 40002, localIP: [4]byte{192, 168, 1, 20}}
}

// TestLANPublicAddrOff: without the setting a LAN member is announced as before.
func TestLANPublicAddrOff(t *testing.T) {
	s := NewServer(10, false)
	m, far := lanMember(), &client{ip: [4]byte{198, 51, 100, 9}}
	if ip, port := s.endpoint(m, far); ip != m.udpIP || port != m.udpPort {
		t.Fatalf("setting off: %v:%d, want the probed endpoint", ip, port)
	}
	if ip, port := endpoint(m, far); ip != m.udpIP || port != m.udpPort {
		t.Fatalf("plain endpoint: %v:%d, want the probed endpoint", ip, port)
	}
}

// TestLANPublicAddrRemoteViewer: a LAN member seen from a public address is
// the configured address and its P2P port.
func TestLANPublicAddrRemoteViewer(t *testing.T) {
	s := lanPublicServer()
	for _, ip := range [][4]byte{{10, 1, 2, 3}, {172, 20, 0, 1}, {192, 168, 0, 5}, {100, 64, 1, 1}, {169, 254, 3, 3}} {
		m := lanMember()
		m.ip = ip
		if got, port := s.endpoint(m, &client{ip: [4]byte{198, 51, 100, 9}}); got != s.lanPublic || port != 3660 {
			t.Fatalf("member %v: %v:%d, want the public address and P2P port", ip, got, port)
		}
	}
}

// TestLANPublicAddrLANViewer: a viewer on a private address keeps the old answer.
func TestLANPublicAddrLANViewer(t *testing.T) {
	s := lanPublicServer()
	m := lanMember()
	near := &client{ip: [4]byte{192, 168, 1, 30}}
	if ip, port := s.endpoint(m, near); ip != m.udpIP || port != m.udpPort {
		t.Fatalf("LAN viewer: %v:%d, want the LAN endpoint", ip, port)
	}
	same := &client{ip: m.ip}
	if ip, port := s.endpoint(m, same); ip != m.localIP || port != m.p2pPort {
		t.Fatalf("same address: %v:%d, want the local address", ip, port)
	}
}

// TestLANPublicAddrPublicMember: a member with a public address is unchanged.
func TestLANPublicAddrPublicMember(t *testing.T) {
	s := lanPublicServer()
	m := &client{ip: [4]byte{203, 0, 113, 7}, p2pPort: 3658, udpIP: [4]byte{203, 0, 113, 7}, udpPort: 40001}
	if ip, port := s.endpoint(m, &client{ip: [4]byte{198, 51, 100, 9}}); ip != m.udpIP || port != 40001 {
		t.Fatalf("public member: %v:%d, want its probed endpoint", ip, port)
	}
}

// TestLANPublicAddrParse: empty is off, one IPv4 address is accepted, anything else is refused.
func TestLANPublicAddrParse(t *testing.T) {
	if ip, err := parseLANPublic("203.0.113.50"); err != nil || ip != [4]byte{203, 0, 113, 50} {
		t.Fatalf("valid: %v %v", ip, err)
	}
	if ip, err := parseLANPublic(""); err != nil || ip != ([4]byte{}) {
		t.Fatalf("empty: %v %v", ip, err)
	}
	for _, bad := range []string{"example.com", "2001:db8::1", "300.1.1.1", "1.2.3"} {
		if _, err := parseLANPublic(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// TestLANPublicAddrBadFlag: a bad value stops the server at start and names the flag.
func TestLANPublicAddrBadFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the server in a subprocess")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(cleanEnv(), "RE_PSN_TEST_MAIN=1", "RE_PSN_ADDR=127.0.0.1:0", "RE_PSN_TEST_ARGS=-lan-public-addr=not-an-ip")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "-lan-public-addr") {
		t.Fatalf("want a refusal naming the flag, got %v:\n%s", err, out)
	}
}

// TestLANPublicAddrFlagAndEnv: the flag and RE_PSN_LAN_PUBLIC_ADDR both set it, and the start-up log says so.
func TestLANPublicAddrFlagAndEnv(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"flag": {nil, []string{"-lan-public-addr", "203.0.113.50"}},
		"env":  {[]string{"RE_PSN_LAN_PUBLIC_ADDR=203.0.113.50"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, log := startMain(t, c.env, c.args...)
			if !strings.Contains(log, "lan public address") || !strings.Contains(log, "203.0.113.50") {
				t.Fatalf("no line in the start-up log:\n%s", log)
			}
		})
	}
}
