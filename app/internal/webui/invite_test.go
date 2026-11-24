package webui

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

func ips(list ...string) addrLister {
	return func() ([]net.IP, error) {
		out := make([]net.IP, 0, len(list))
		for _, s := range list {
			out = append(out, net.ParseIP(s))
		}
		return out, nil
	}
}

func TestAddressKind(t *testing.T) {
	for ip, want := range map[string]string{
		"127.0.0.1":       "loopback",
		"::1":             "loopback",
		"169.254.10.20":   "link-local",
		"fe80::1":         "link-local",
		"10.1.2.3":        "private",
		"172.16.0.5":      "private",
		"172.31.255.1":    "private",
		"172.32.0.1":      "other",
		"192.168.1.20":    "private",
		"fd12:3456::7":    "private",
		"fc00::1":         "private",
		"192.0.2.10":      "other",
		"198.51.100.7":    "other",
		"203.0.113.9":     "other",
		"100.64.0.1":      "shared",
		"100.127.255.254": "shared",
		"100.128.0.1":     "other",
		"2001:db8::1":     "other",
		"::ffff:10.0.0.1": "private",
	} {
		if got := addressKind(net.ParseIP(ip)); got != want {
			t.Errorf("%s is %s, want %s", ip, got, want)
		}
	}
}

func TestBuildInviteWildcardTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		bind string
		list addrLister
		want []InviteAddress
	}{
		{"every kind, sorted", "0.0.0.0:36100", ips("127.0.0.1", "169.254.3.4", "192.0.2.5", "192.168.1.2", "fe80::1", "10.0.0.8", "172.16.4.4"), []InviteAddress{
			{"192.168.1.2:36100", "private"}, {"10.0.0.8:36100", "private"}, {"172.16.4.4:36100", "private"},
			{"192.0.2.5:36100", "other"}, {"169.254.3.4:36100", "link-local"}, {"127.0.0.1:36100", "loopback"}}},
		{"IPv6 too on [::]", "[::]:36100", ips("fe80::1", "fd00::2", "2001:db8::3", "198.51.100.1", "::1"), []InviteAddress{
			{"[fd00::2]:36100", "private"}, {"[2001:db8::3]:36100", "other"}, {"198.51.100.1:36100", "other"}, {"[fe80::1]:36100", "link-local"}, {"[::1]:36100", "loopback"}}},
		{"unique-local and mapped", "[::]:1", ips("fc00::9", "::ffff:10.0.0.1"), []InviteAddress{{"[fc00::9]:1", "private"}, {"10.0.0.1:1", "private"}}},
		{"0.0.0.0 skips IPv6", "0.0.0.0:2", ips("fd00::1", "192.168.0.9"), []InviteAddress{{"192.168.0.9:2", "private"}}},
		{"duplicates and noise", "0.0.0.0:3", ips("192.168.0.9", "192.168.0.9", "0.0.0.0", "224.0.0.1", "::ffff:192.168.0.9"), []InviteAddress{{"192.168.0.9:3", "private"}}},
		{"empty list", "0.0.0.0:36100", ips(), []InviteAddress{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildInvite(tc.bind, tc.list)
			if err != nil {
				t.Fatal(err)
			}
			if !got.AllInterfaces || got.ListFailed || got.LoopbackOnly {
				t.Fatalf("flags %+v", got)
			}
			if !reflect.DeepEqual(got.Addresses, tc.want) {
				t.Fatalf("got %v, want %v", got.Addresses, tc.want)
			}
		})
	}
}

func TestBuildInviteSpecificBindAndFailures(t *testing.T) {
	never := func() ([]net.IP, error) { t.Fatal("a specific bind must not read the interface list"); return nil, nil }
	got, err := buildInvite("192.168.1.2:36100", never)
	if err != nil || got.AllInterfaces || got.LoopbackOnly || got.Bind != "192.168.1.2:36100" || got.Port != "36100" || !reflect.DeepEqual(got.Addresses, []InviteAddress{{"192.168.1.2:36100", "private"}}) {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ = buildInvite("[::1]:36100", never); !got.LoopbackOnly || got.Addresses[0].Kind != "loopback" {
		t.Fatalf("%+v", got)
	}
	if got, _ = buildInvite("localhost:36100", never); !got.LoopbackOnly || got.Addresses[0].Kind != "loopback" {
		t.Fatalf("%+v", got)
	}
	if got, _ = buildInvite("0.0.0.0:36100", func() ([]net.IP, error) { return nil, errors.New("no interfaces") }); !got.ListFailed || len(got.Addresses) != 0 || got.Addresses == nil {
		t.Fatalf("%+v", got)
	}
	if got, _ = buildInvite("0.0.0.0:36100", nil); !got.ListFailed {
		t.Fatalf("%+v", got)
	}
	if _, err = buildInvite("no-port", never); err == nil {
		t.Fatal("a bind without a port was accepted")
	}
}

func TestBuildInviteCapsTheList(t *testing.T) {
	var list []string
	for i := 1; i <= 20; i++ {
		list = append(list, net.IPv4(10, 0, 0, byte(i)).String())
	}
	got, _ := buildInvite("0.0.0.0:1", ips(list...))
	if len(got.Addresses) != maxInviteAddresses || !got.Truncated {
		t.Fatalf("%d addresses, truncated %v", len(got.Addresses), got.Truncated)
	}
}

func TestLoopbackBind(t *testing.T) {
	for bind, want := range map[string]bool{
		"127.0.0.1:36100": true, "127.0.0.2:1": true, "[::1]:36100": true, "localhost:1": true,
		"0.0.0.0:36100": false, "[::]:36100": false, "192.168.1.2:1": false, "192.0.2.1:1": false, "nonsense": false, "": false,
	} {
		if got := loopbackBind(bind); got != want {
			t.Errorf("%q is %v, want %v", bind, got, want)
		}
	}
	got, _ := buildInvite("127.0.0.1:36100", nil)
	if !got.LoopbackOnly {
		t.Fatal("a loopback bind did not set the warning flag")
	}
}

func TestInviteRouteNeedsCapabilityAndRunningHost(t *testing.T) {
	f := serveUI(t)
	f.server.addrs = func() ([]net.IP, error) { t.Error("no host is running, so nothing may be listed"); return nil, nil }
	callUI(t, f, "", "/api/host/invite", struct{}{}, 401)
	callUI(t, f, f.nonce, "/api/host/invite", struct{}{}, 401)
	callUI(t, f, f.token, "/api/host/invite", struct{}{}, 403) // no workspace chosen yet
	saveRole(t, f, "play", nil)
	callUI(t, f, f.token, "/api/host/invite", struct{}{}, 403)
	saveRole(t, f, "host", nil)
	callUI(t, f, f.token, "/api/host/invite", struct{}{}, 400) // allowed role, but the owned host is not running
}

func TestBuildInviteSpecificIPv6BindIsBracketed(t *testing.T) {
	never := func() ([]net.IP, error) { t.Fatal("a specific bind must not read the interface list"); return nil, nil }
	for bind, want := range map[string]InviteAddress{
		"[fd00::2]:36100":   {"[fd00::2]:36100", "private"},
		"[2001:db8::1]:7":   {"[2001:db8::1]:7", "other"},
		"[fe80::1]:36100":   {"[fe80::1]:36100", "link-local"},
		"198.51.100.4:9000": {"198.51.100.4:9000", "other"},
		"100.100.1.2:36100": {"100.100.1.2:36100", "shared"},
		"[fe80::1%eth0]:7":  {"[fe80::1%eth0]:7", "unknown"},
	} {
		got, err := buildInvite(bind, never)
		if err != nil || got.LoopbackOnly || got.AllInterfaces || !reflect.DeepEqual(got.Addresses, []InviteAddress{want}) {
			t.Errorf("%s: %+v %v", bind, got, err)
		}
	}
}
