// Package integration tests a native game caller, the app store and real TLS
// companions together. All metadata, identities and certificates are generated.
package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/framework"
)

func nativeExample(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native sample runtime is checked on POSIX; Windows requires ws2_32")
	}
	ccName := os.Getenv("CC")
	if ccName == "" {
		ccName = "cc"
	}
	cc, err := exec.LookPath(ccName)
	if err != nil {
		t.Skip("C compiler unavailable")
	}
	root := filepath.Join("..", "..")
	binary := filepath.Join(t.TempDir(), "app-trophy")
	cmd := exec.Command(cc, "-std=c99", "-Wall", "-Wextra", "-Werror", "-I"+filepath.Join(root, "client"), filepath.Join(root, "client/psnr_app.c"), filepath.Join(root, "client/examples/app_trophy.c"), "-o", binary)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native build: %v\n%s", err, b)
	}
	cppName := os.Getenv("CXX")
	if cppName == "" {
		cppName = "c++"
	}
	cpp, err := exec.LookPath(cppName)
	if err != nil {
		t.Fatal("C++ compiler required for SDK ABI check")
	}
	source := filepath.Join(t.TempDir(), "header.cpp")
	if err := os.WriteFile(source, []byte("#include \"psnr_app.h\"\nint main(){psnr_app_client c{};psnr_app_trophy t{};return psnr_app_status(&c,0,&t)==PSNR_APP_INVALID_ARGUMENT?0:1;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(t.TempDir(), "sdk.o")
	if b, e := exec.Command(cc, "-std=c99", "-I"+filepath.Join(root, "client"), "-c", filepath.Join(root, "client/psnr_app.c"), "-o", object).CombinedOutput(); e != nil {
		t.Fatalf("C object: %v %s", e, b)
	}
	cppbin := filepath.Join(t.TempDir(), "cpp-abi")
	if b, e := exec.Command(cpp, "-std=c++17", "-Wall", "-Wextra", "-Werror", "-I"+filepath.Join(root, "client"), source, object, "-o", cppbin).CombinedOutput(); e != nil {
		t.Fatalf("C++ ABI: %v %s", e, b)
	}
	if b, e := exec.Command(cppbin).CombinedOutput(); e != nil {
		t.Fatalf("C++ ABI run: %v %s", e, b)
	}
	return binary
}

func writeGrant(t *testing.T, port int, token [32]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grant.txt")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("re-psn-app-grant-v1\nport=%d\ntoken=%s\n", port, hex.EncodeToString(token[:]))), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func syntheticCatalog() framework.Catalog {
	return framework.Catalog{TitleID: "SYNTH-ALPHA", Name: "Synthetic developer fixture", Revision: "1", Trophies: []framework.Trophy{{ID: 0, Name: "A synthetic unlock", Description: "Generated fixture only", Grade: "bronze"}, {ID: 1, Name: "Explicit platinum", Grade: "platinum"}}}
}

func TestNativeSDKRejectsHostileDescriptorsAndReplies(t *testing.T) {
	binary := nativeExample(t)
	invoke := func(path string) (string, error, time.Duration) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		start := time.Now()
		b, err := exec.CommandContext(ctx, binary, path, "status", "1").CombinedOutput()
		return string(b), err, time.Since(start)
	}
	if runtime.GOOS == "linux" {
		fifo := filepath.Join(t.TempDir(), "fifo")
		if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
			t.Fatalf("FIFO fixture: %v %s", err, out)
		}
		if out, err, elapsed := invoke(fifo); err == nil || !strings.Contains(out, "(101)") || elapsed > time.Second {
			t.Fatalf("non-regular descriptor blocked or was accepted: %q %v %s", out, err, elapsed)
		}
	}
	for _, tc := range []struct {
		name, reply, want string
		drip              bool
	}{
		{"wrong magic", "NOPE\x00\x00\x00\x00\x00\x00\x00\x09" + strings.Repeat("\x00", 9), "(102)", false},
		{"invalid boolean", "RPR1\x00\x00\x00\x00\x00\x00\x00\x09\x02" + strings.Repeat("\x00", 8), "(102)", false},
		{"trailing response", "RPR1\x00\x00\x00\x00\x00\x00\x00\x09" + strings.Repeat("\x00", 9) + "extra", "(102)", false},
		{"absolute deadline", "RPR1\x00\x00\x00\x00\x00\x00\x00\x09" + strings.Repeat("\x00", 9), "(103)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(4 * time.Second))
				if _, err := io.Copy(io.Discard, io.LimitReader(c, 49)); err != nil {
					return
				}
				for _, b := range []byte(tc.reply) {
					if tc.drip {
						time.Sleep(150 * time.Millisecond)
					}
					if _, err := c.Write([]byte{b}); err != nil {
						return
					}
				}
			}()
			path := writeGrant(t, ln.Addr().(*net.TCPAddr).Port, [32]byte{1})
			out, err, elapsed := invoke(path)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("accepted hostile response: %q %v", out, err)
			}
			if tc.drip && (elapsed < time.Second || elapsed > 3*time.Second) {
				t.Fatalf("deadline was reset by progress: %s", elapsed)
			}
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("fake peer did not stop")
			}
		})
	}
}

func TestNativeTrophyAndPortableFriends(t *testing.T) {
	binary := nativeExample(t)
	dir := filepath.Join(t.TempDir(), "alice")
	alice, err := framework.Create(dir, "Alice synthetic")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { alice.Close() })
	if err := alice.InstallCatalog(syntheticCatalog()); err != nil {
		t.Fatal(err)
	}
	other := syntheticCatalog()
	other.TitleID = "SYNTH-BETA"
	if err := alice.InstallCatalog(other); err != nil {
		t.Fatal(err)
	}
	broker := framework.NewBroker(alice)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- broker.Serve(ln) }()
	t.Cleanup(func() { broker.Close(); ln.Close() })
	grant, err := broker.Grant("SYNTH-ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	path := writeGrant(t, ln.Addr().(*net.TCPAddr).Port, grant.Token)
	call := func(action, id string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b, e := exec.CommandContext(ctx, binary, path, action, id).CombinedOutput()
		return strings.TrimSpace(string(b)), e
	}
	if b, e := call("status", "0"); e != nil || b != "0 0 0" {
		t.Fatalf("initial state: %q %v", b, e)
	}
	first, e := call("unlock", "0")
	if e != nil || !strings.HasPrefix(first, "0 1 ") {
		t.Fatalf("native unlock: %q %v", first, e)
	}
	if again, e := call("unlock", "0"); e != nil || again != first {
		t.Fatalf("idempotent unlock: %q vs %q %v", again, first, e)
	}
	if b, e := call("status", "99"); e == nil || !strings.Contains(b, "(2)") {
		t.Fatalf("unknown trophy: %q %v", b, e)
	}
	if u, e := alice.Status("SYNTH-BETA", 0); e != nil || u.Unlocked {
		t.Fatal("grant crossed title boundary", e)
	}
	broker.Revoke(grant.Token)
	if b, e := call("unlock", "1"); e == nil || !strings.Contains(b, "(1)") {
		t.Fatalf("revoked grant: %q %v", b, e)
	}
	if u, e := alice.Status("SYNTH-ALPHA", 1); e != nil || u.Unlocked {
		t.Fatal("revoked report mutated store", e)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("broker did not stop")
	}
	account := alice.View().AccountID
	if err := alice.Close(); err != nil {
		t.Fatal(err)
	}
	alice, err = framework.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if u, e := alice.Status("SYNTH-ALPHA", 0); e != nil || !u.Unlocked || !strings.Contains(first, fmt.Sprint(u.UnlockedAt)) {
		t.Fatal("native unlock lost after restart", e)
	}
	var archive bytes.Buffer
	if err := alice.Export(&archive, []byte("synthetic backup passphrase")); err != nil {
		t.Fatal(err)
	}
	restored, err := framework.Restore(filepath.Join(t.TempDir(), "restored"), bytes.NewReader(archive.Bytes()), []byte("synthetic backup passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.View().AccountID != account || len(restored.View().Relays) != 0 {
		t.Fatal("restore changed identity or installed remote authority")
	}
	bob, err := framework.Create(filepath.Join(t.TempDir(), "bob"), "Bob synthetic")
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	allowed := []string{account, bob.View().AccountID}
	one := startSocial(t, filepath.Join(t.TempDir(), "relay-one"), allowed)
	two := startSocial(t, filepath.Join(t.TempDir(), "relay-two"), allowed)
	client := func(s *framework.Store, h socialHost) *framework.SocialClient {
		t.Helper()
		c, e := framework.NewSocialClient(s, framework.SocialClientConfig{Origin: h.origin, Roots: h.roots})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	a1, a2, b1, b2 := client(restored, one), client(restored, two), client(bob, one), client(bob, two)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, c := range []*framework.SocialClient{a1, a2, b1, b2} {
		if _, e := c.Register(ctx, "Synthetic player"); e != nil {
			t.Fatal("registration", e)
		}
	}
	if state, e := a1.Request(ctx, bob.View().AccountID); e != nil || len(state.Friends) != 0 || len(state.Outgoing) != 1 {
		t.Fatal("request granted friendship", e)
	}
	if _, e := a1.Accept(ctx, bob.View().AccountID); e == nil {
		t.Fatal("sender accepted own outgoing request")
	}
	if state, e := b1.Accept(ctx, account); e != nil || len(state.Friends) != 1 {
		t.Fatal("accept", e)
	}
	if state, e := a2.State(ctx); e != nil || len(state.Friends) != 0 {
		t.Fatal("friendship crossed relay boundary", e)
	}
	if state, e := a1.Block(ctx, bob.View().AccountID); e != nil || len(state.Friends) != 0 {
		t.Fatal("block", e)
	}
	if state, e := b1.State(ctx); e != nil || len(state.Friends) != 0 {
		t.Fatal("block left peer friendship", e)
	}
	if state, e := a1.Unblock(ctx, bob.View().AccountID); e != nil || len(state.Friends) != 0 {
		t.Fatal("unblock resurrected friendship", e)
	}
	if _, e := a1.Request(ctx, bob.View().AccountID); e != nil {
		t.Fatal(e)
	}
	if _, e := b1.Accept(ctx, account); e != nil {
		t.Fatal(e)
	}
	if err := one.service.RevokeAccount(bob.View().AccountID); err != nil {
		t.Fatal(err)
	}
	if _, e := b1.State(ctx); e == nil {
		t.Fatal("revoked peer retained access")
	}
	if state, e := a1.State(ctx); e != nil || len(state.Friends) != 0 {
		t.Fatal("revocation left current friendship", e)
	}
	if state, e := a2.State(ctx); e != nil || len(state.Friends) != 0 {
		t.Fatal("other relay changed", e)
	}
}

type socialHost struct {
	origin  string
	roots   *x509.CertPool
	service *framework.SocialService
}

func startSocial(t *testing.T, dir string, ids []string) socialHost {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "Synthetic relay"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	service, err := framework.OpenSocialService(framework.SocialConfig{StateDir: dir, AllowedIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- service.ServeTLS(ln, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert})
	}()
	t.Cleanup(func() {
		service.Close()
		ln.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("social service did not stop")
		}
	})
	return socialHost{origin: "https://" + ln.Addr().String(), roots: roots, service: service}
}
