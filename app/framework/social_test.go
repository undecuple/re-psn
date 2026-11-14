package framework

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func serverCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic fixture"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}
func startSocial(t *testing.T, dir string, ids []string) (*SocialService, string, *x509.CertPool, func()) {
	t.Helper()
	s, err := OpenSocialService(SocialConfig{StateDir: dir, AllowedIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	cert, roots := serverCertificate(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.ServeTLS(l, cert) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			s.Close()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return s, "https://" + l.Addr().String(), roots, stop
}
func socialClient(t *testing.T, s *Store, origin string, roots *x509.CertPool) *SocialClient {
	t.Helper()
	c, err := NewSocialClient(s, SocialClientConfig{Origin: origin, Roots: roots})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func mustSocial(t *testing.T, v SocialState, err error) SocialState {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestSocialTwoRelaysConsentPersistenceAndRestore(t *testing.T) {
	a, b := fixtureStore(t, "Synthetic A"), fixtureStore(t, "Synthetic B")
	aid, bid := a.View().AccountID, b.View().AccountID
	ids := []string{aid, bid}
	dir := privateTempDir(t)
	_, origin, roots, stop := startSocial(t, dir, ids)
	_, other, otherRoots, _ := startSocial(t, privateTempDir(t), ids)
	ac, bc := socialClient(t, a, origin, roots), socialClient(t, b, origin, roots)
	ao, bo := socialClient(t, a, other, otherRoots), socialClient(t, b, other, otherRoots)
	ctx := context.Background()
	for _, p := range []struct {
		c     *SocialClient
		label string
	}{{ac, "Synthetic A"}, {bc, "Synthetic B"}, {ao, "Synthetic A"}, {bo, "Synthetic B"}} {
		v, err := p.c.Register(ctx, p.label)
		mustSocial(t, v, err)
	}
	v, err := ac.Request(ctx, bid)
	v = mustSocial(t, v, err)
	if len(v.Outgoing) != 1 || len(v.Friends) != 0 {
		t.Fatal(v)
	}
	if _, err := ac.Accept(ctx, bid); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("outgoing self-accept", err)
	}
	if _, err := bc.Request(ctx, aid); !errors.Is(err, ErrBusy) {
		t.Fatal("crossed request auto-accepted", err)
	}
	v, err = bc.Accept(ctx, aid)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 1 {
		t.Fatal(v)
	}
	v, err = ao.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 || len(v.Incoming) != 0 {
		t.Fatal("cross-relay relationship transfer", v)
	}
	var archive bytes.Buffer
	pass := []byte("synthetic backup passphrase")
	if err := a.Export(&archive, pass); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(privateTempDir(t), bytes.NewReader(archive.Bytes()), pass)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.View().Relays) != 0 || restored.View().AccountID != aid {
		t.Fatal("restored authority cache", restored.View())
	}
	rc := socialClient(t, restored, origin, roots)
	v, err = rc.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 1 {
		t.Fatal("restored identity did not authenticate", v)
	}
	v, err = bc.Block(ctx, aid)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 || len(v.Blocked) != 1 {
		t.Fatal(v)
	}
	if _, err := ac.Request(ctx, bid); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("block bypass", err)
	}
	ac.Close()
	bc.Close()
	rc.Close()
	stop()
	_, origin2, roots2, _ := startSocial(t, dir, ids)
	ac2, bc2 := socialClient(t, a, origin2, roots2), socialClient(t, b, origin2, roots2)
	v, err = bc2.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Blocked) != 1 || len(v.Friends) != 0 {
		t.Fatal("block not durable", v)
	}
	v, err = bc2.Unblock(ctx, aid)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 || len(v.Incoming) != 0 {
		t.Fatal("unblock restored friendship", v)
	}
	v, err = ac2.Request(ctx, bid)
	mustSocial(t, v, err)
	v, err = bc2.Decline(ctx, aid)
	mustSocial(t, v, err)
	v, err = ac2.Request(ctx, bid)
	mustSocial(t, v, err)
	v, err = ac2.Cancel(ctx, bid)
	mustSocial(t, v, err)
	v, err = ac2.Request(ctx, bid)
	mustSocial(t, v, err)
	v, err = bc2.Accept(ctx, aid)
	mustSocial(t, v, err)
	v, err = ac2.Remove(ctx, bid)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 {
		t.Fatal(v)
	}
	v, err = bc2.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 {
		t.Fatal("unfriend not mutual", v)
	}
}
func TestSocialRevocationKeepAliveResumptionAndEnrollment(t *testing.T) {
	a, b, unlisted := fixtureStore(t, "Synthetic A"), fixtureStore(t, "Synthetic B"), fixtureStore(t, "Synthetic denied")
	ids := []string{a.View().AccountID, b.View().AccountID}
	dir := privateTempDir(t)
	svc, origin, roots, stop := startSocial(t, dir, ids)
	ac, bc := socialClient(t, a, origin, roots), socialClient(t, b, origin, roots)
	denied := socialClient(t, unlisted, origin, roots)
	ctx := context.Background()
	ac.transport.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(1)
	var resumed atomic.Bool
	ac.transport.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if cs.DidResume {
			resumed.Store(true)
		}
		return nil
	}
	if _, err := denied.Register(ctx, "Synthetic denied"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("default deny enrollment", err)
	}
	v, err := ac.Register(ctx, "Synthetic A")
	mustSocial(t, v, err)
	v, err = bc.Register(ctx, "Synthetic B")
	mustSocial(t, v, err)
	v, err = ac.Request(ctx, b.View().AccountID)
	mustSocial(t, v, err)
	v, err = bc.Accept(ctx, a.View().AccountID)
	mustSocial(t, v, err)
	if err := svc.RevokeAccount(a.View().AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := ac.State(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("keepalive bypassed revocation", err)
	}
	ac.transport.CloseIdleConnections()
	if _, err := ac.State(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("resumption bypassed revocation", err)
	}
	if !resumed.Load() {
		t.Fatal("resumption control did not resume TLS")
	}
	v, err = bc.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 {
		t.Fatal("revoked peer visible", v)
	}
	ac.Close()
	bc.Close()
	stop()
	_, origin2, roots2, _ := startSocial(t, dir, ids)
	again := socialClient(t, a, origin2, roots2)
	if _, err := again.Register(ctx, "Synthetic A"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("restart resurrected revoked account", err)
	}
}
func TestSocialTLSIdentityAndEndpointBounds(t *testing.T) {
	a := fixtureStore(t, "Synthetic A")
	_, origin, roots, _ := startSocial(t, privateTempDir(t), []string{a.View().AccountID})
	ctx := context.Background()
	untrusted := socialClient(t, a, origin, x509.NewCertPool())
	if _, err := untrusted.Register(ctx, "Synthetic A"); err == nil {
		t.Fatal("accepted untrusted server")
	}
	wrongName := socialClient(t, a, origin, roots)
	wrongName.transport.TLSClientConfig.ServerName = "wrong.example.invalid"
	if _, err := wrongName.Register(ctx, "Synthetic A"); err == nil {
		t.Fatal("accepted wrong server hostname")
	}
	other := fixtureStore(t, "Synthetic forged label")
	forged := socialClient(t, other, origin, roots)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: a.View().AccountID}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, forged.key.Public(), forged.key)
	if err != nil {
		t.Fatal(err)
	}
	forged.transport.TLSClientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: forged.key}}
	if _, err := forged.Register(ctx, "Synthetic forged label"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("certificate subject granted another account", err)
	}
	for _, bad := range []string{"http://127.0.0.1", "https://name.invalid/path", "https://user:password@name.invalid", "https://name.invalid?x=1", "https://name.invalid#fragment"} {
		if _, err := NewSocialClient(a, SocialClientConfig{Origin: bad, Roots: roots}); err == nil {
			t.Fatal("accepted origin", bad)
		}
	}
	c := socialClient(t, a, origin, roots)
	v, err := c.Register(ctx, "Synthetic A")
	mustSocial(t, v, err)
	if _, err := c.Register(ctx, strings.Repeat("x", 129)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(c.transport.TLSClientConfig.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	state := tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}}
	id, err := clientIdentity(state)
	if err != nil || id != a.View().AccountID {
		t.Fatal(id, err)
	}
	copyCert := *cert
	copyCert.NotAfter = time.Now().Add(-time.Second)
	state.PeerCertificates = []*x509.Certificate{&copyCert}
	if _, err := clientIdentity(state); err == nil {
		t.Fatal("expired client cert accepted")
	}
	state.Version = tls.VersionTLS12
	if _, err := clientIdentity(state); err == nil {
		t.Fatal("old TLS accepted")
	}
}
func TestSocialAcceptBlockRace(t *testing.T) {
	a, b := fixtureStore(t, "Synthetic race A"), fixtureStore(t, "Synthetic race B")
	_, origin, roots, _ := startSocial(t, privateTempDir(t), []string{a.View().AccountID, b.View().AccountID})
	ac, bc := socialClient(t, a, origin, roots), socialClient(t, b, origin, roots)
	ctx := context.Background()
	v, err := ac.Register(ctx, "Synthetic race A")
	mustSocial(t, v, err)
	v, err = bc.Register(ctx, "Synthetic race B")
	mustSocial(t, v, err)
	v, err = ac.Request(ctx, b.View().AccountID)
	mustSocial(t, v, err)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := bc.Accept(ctx, a.View().AccountID)
		if err != nil && !errors.Is(err, ErrUnauthorized) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		_, err := ac.Block(ctx, b.View().AccountID)
		if err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	v, err = ac.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 || len(v.Blocked) != 1 || len(v.Outgoing) != 0 {
		t.Fatal(v)
	}
	v, err = bc.State(ctx)
	v = mustSocial(t, v, err)
	if len(v.Friends) != 0 || len(v.Incoming) != 0 {
		t.Fatal(v)
	}
}

func TestSocialQueuedRequestCancellation(t *testing.T) {
	s := fixtureStore(t, "Synthetic canceled player")
	c, err := NewSocialClient(s, SocialClientConfig{Origin: "https://example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	c.gate <- struct{}{} // a preceding request owns the serializer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.State(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request stayed queued")
	}
	<-c.gate
	c.Close()
}

func TestSocialHTTPInputBoundaries(t *testing.T) {
	s := fixtureStore(t, "Synthetic HTTP player")
	_, origin, roots, _ := startSocial(t, privateTempDir(t), []string{s.View().AccountID})
	c := socialClient(t, s, origin, roots)
	for _, tc := range []struct {
		name, method, path, body, origin string
		want                             int
	}{
		{"duplicate", "POST", "/v1/register", `{"label":"first","label":"second"}`, "", 400},
		{"unknown", "POST", "/v1/register", `{"label":"Synthetic","unknown":true}`, "", 400},
		{"oversize", "POST", "/v1/register", strings.Repeat("x", 1025), "", 400},
		{"browser", "POST", "/v1/register", `{"label":"Synthetic"}`, "https://example.invalid", 403},
		{"statebody", "GET", "/v1/state", `{}`, "", 400},
		{"wrongmethod", "DELETE", "/v1/state", "", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, origin+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			resp, err := c.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != tc.want {
				t.Fatal(resp.StatusCode)
			}
		})
	}
}

func TestSocialUnblockRevokedPeerFreesQuota(t *testing.T) {
	a, b := fixtureStore(t, "Synthetic cleanup A"), fixtureStore(t, "Synthetic cleanup B")
	aid, bid := a.View().AccountID, b.View().AccountID
	svc, origin, roots, _ := startSocial(t, privateTempDir(t), []string{aid, bid})
	ac, bc := socialClient(t, a, origin, roots), socialClient(t, b, origin, roots)
	ctx := context.Background()
	v, err := ac.Register(ctx, "Synthetic cleanup A")
	mustSocial(t, v, err)
	v, err = bc.Register(ctx, "Synthetic cleanup B")
	mustSocial(t, v, err)
	v, err = ac.Block(ctx, bid)
	v = mustSocial(t, v, err)
	if len(v.Blocked) != 1 {
		t.Fatal(v)
	}
	if err := svc.RevokeAccount(bid); err != nil {
		t.Fatal(err)
	}
	v, err = ac.Unblock(ctx, bid)
	v = mustSocial(t, v, err)
	if len(v.Blocked) != 0 || len(v.Friends) != 0 {
		t.Fatal("stale block quota was not freed", v)
	}
	if _, err := ac.Request(ctx, bid); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("cleanup resurrected revoked peer", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func syntheticResponse(id string, revision uint64) *http.Response {
	b, _ := json.Marshal(SocialState{AccountID: id, Label: "Synthetic cache player", Revision: revision})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}
}
func TestSocialClientsShareProfileRequestOrdering(t *testing.T) {
	s := fixtureStore(t, "Synthetic cache player")
	if err := s.InstallCatalog(fixtureCatalog("SYNTHETIC")); err != nil {
		t.Fatal(err)
	}
	one := socialClient(t, s, "https://example.invalid", nil)
	two := socialClient(t, s, "https://example.invalid", nil)
	id := s.View().AccountID
	firstEntered, secondEntered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	one.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(firstEntered)
		select {
		case <-release:
			return syntheticResponse(id, 1), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	two.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(secondEntered)
		return syntheticResponse(id, 2), nil
	})
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := one.State(context.Background()); firstDone <- err }()
	<-firstEntered
	go func() { _, err := two.State(context.Background()); secondDone <- err }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(two.gate) == 0 {
		select {
		case <-secondEntered:
			t.Fatal("second client overtook outstanding response")
		case <-deadline.C:
			t.Fatal("second client did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case <-secondEntered:
		t.Fatal("second client overtook outstanding response")
	case <-time.After(50 * time.Millisecond):
	}
	// Network serialization must not hold the trophy store's state lock.
	trophyDone := make(chan error, 1)
	go func() { _, err := s.Unlock("SYNTHETIC", 7); trophyDone <- err }()
	select {
	case err := <-trophyDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("social network wait blocked local trophy writes")
	}
	unblock()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got := s.View().Relays[0].State.Revision; got != 2 {
		t.Fatal("older response overwrote newer cache", got)
	}
}
func TestSocialSharedProfileQueueCancellation(t *testing.T) {
	s := fixtureStore(t, "Synthetic shared cancellation")
	c := socialClient(t, s, "https://example.invalid", nil)
	s.socialGate <- struct{}{}
	defer func() { <-s.socialGate }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.State(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request stayed in shared profile queue")
	}
}
