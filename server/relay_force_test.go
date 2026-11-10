package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRelayForceRule: with -relay-force every stream uses the relay, even
// between players on one network or one with a public address; without -relay
// it changes nothing.
func TestRelayForceRule(t *testing.T) {
	s := NewServer(10, false)
	s.relay, s.relayForce = true, true
	open := &client{ip: [4]byte{198, 51, 100, 9}, udpIP: [4]byte{198, 51, 100, 9}, udpPort: 3658,
		localIP: [4]byte{198, 51, 100, 9}}
	sameHouse := &client{ip: [4]byte{198, 51, 100, 9}}
	if !s.relayStream(open, sameHouse) {
		t.Fatal("-relay-force: a stream between two players on one network should use the relay")
	}
	if !s.relayStream(open, &client{}) {
		t.Fatal("-relay-force: a stream to a player with a public address should use the relay")
	}
	s.relay = false
	if s.relayStream(open, sameHouse) {
		t.Fatal("-relay-force does nothing without -relay")
	}
}

// TestRoutePushForced: with -relay-force a join tells both sides to stream
// through the relay, though both are on the same address.
func TestRoutePushForced(t *testing.T) {
	_, addr := startRelay(t, true)
	s, addrForce := startRelay(t, true)
	s.relayForce = true
	for _, c := range []struct {
		addr string
		want uint8
	}{{addr, 0}, {addrForce, routeStreamRelay}} {
		a, b := join(t, c.addr, "alice"), join(t, c.addr, "bob")
		meet(t, a, b)
		r := b.c.next(msgRoute)
		r.u64()
		if who, flags := r.u32(), r.u8(); who != a.id || flags != c.want {
			t.Fatalf("route about %d flags %d, want %d", who, flags, c.want)
		}
	}
}

// TestRelayForceNeedsRelay: -relay-force alone stops the server at start and
// says why.
func TestRelayForceNeedsRelay(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the server in a subprocess")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(cleanEnv(), "RE_PSN_TEST_MAIN=1", "RE_PSN_ADDR=127.0.0.1:0", "RE_PSN_TEST_ARGS=-relay-force")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "-relay-force needs -relay") {
		t.Fatalf("want a refusal naming -relay, got %v:\n%s", err, out)
	}
}

// TestRelayForceFlagAndEnv: the flag and RE_PSN_RELAY_FORCE both turn it on,
// and the start-up log warns that it is for testing.
func TestRelayForceFlagAndEnv(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"flag": {[]string{"RE_PSN_RELAY=true"}, []string{"-relay-force"}},
		"env":  {[]string{"RE_PSN_RELAY=true", "RE_PSN_RELAY_FORCE=true"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, log := startMain(t, c.env, c.args...)
			if !strings.Contains(log, "-relay-force") || !strings.Contains(log, "testing only") {
				t.Fatalf("no warning in the start-up log:\n%s", log)
			}
		})
	}
}
