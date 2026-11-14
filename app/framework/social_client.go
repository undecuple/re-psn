package framework

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SocialClientConfig struct {
	Origin string
	Roots  *x509.CertPool
}

// CacheError means the remote operation succeeded but its local cache could not
// be saved. Refresh remote state; do not assume the operation rolled back.
type CacheError struct{ Err error }

func (e *CacheError) Error() string {
	return "framework: remote operation succeeded; local cache unavailable"
}
func (e *CacheError) Unwrap() error { return e.Err }

type SocialClient struct {
	gate      chan struct{}
	store     *Store
	origin    string
	id        string
	client    *http.Client
	transport *http.Transport
	key       ed25519.PrivateKey
	closed    bool
}

func validOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && (u.Path == "" || u.Path == "/") && u.RawPath == "" && !strings.ContainsAny(origin, "\r\n\t ")
}
func NewSocialClient(store *Store, cfg SocialClientConfig) (*SocialClient, error) {
	if !validOrigin(cfg.Origin) {
		return nil, ErrInvalid
	}
	key, err := store.key()
	if err != nil {
		return nil, err
	}
	pub := key.Public().(ed25519.PublicKey)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		clear(key)
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		clear(key)
		return nil, err
	}
	var roots *x509.CertPool
	if cfg.Roots != nil {
		roots = cfg.Roots.Clone()
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, NextProtos: []string{"http/1.1"}}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSClientConfig: tc, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxResponseHeaderBytes: 8192, MaxConnsPerHost: 1, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 5 * time.Second}
	return &SocialClient{gate: make(chan struct{}, 1), store: store, origin: strings.TrimSuffix(cfg.Origin, "/"), id: accountID(pub), key: key, transport: tr, client: &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *SocialClient) Close() error {
	c.gate <- struct{}{}
	defer func() { <-c.gate }()
	if c.closed {
		return nil
	}
	c.closed = true
	c.transport.CloseIdleConnections()
	// Transport can finish a canceled dial asynchronously. Do not mutate a key
	// array still referenced by crypto/tls; release our reference for GC instead.
	c.key = nil
	return nil
}
func (c *SocialClient) call(ctx context.Context, op string, q socialRequest) (SocialState, error) {
	var state SocialState
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return state, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if c.closed {
		return state, ErrClosed
	}
	// Separate clients for the same open profile share ordering through the
	// remote response and local cache commit. This gate never locks trophies.
	select {
	case c.store.socialGate <- struct{}{}:
		defer func() { <-c.store.socialGate }()
	case <-ctx.Done():
		return state, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	c.store.mu.Lock()
	closed := c.store.closed
	c.store.mu.Unlock()
	if closed {
		return state, ErrClosed
	}
	method := http.MethodPost
	b, err := json.Marshal(q)
	if err != nil {
		return state, ErrInvalid
	}
	if op == "state" {
		method = http.MethodGet
		b = nil
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+"/v1/"+op, bytes.NewReader(b))
	if err != nil {
		return state, ErrInvalid
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return state, ctx.Err()
		}
		return state, errors.New("framework: social connection unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 401, 403:
			return state, ErrUnauthorized
		case 409, 429:
			return state, ErrBusy
		case 400:
			return state, ErrInvalid
		default:
			return state, fmt.Errorf("framework: social request failed (HTTP %d)", resp.StatusCode)
		}
	}
	body, err := boundedRead(resp.Body, 128<<10)
	if err != nil || strictJSON(body, &state) != nil || state.AccountID != c.id || !validSocialState(state) {
		return SocialState{}, ErrInvalid
	}
	if err := c.store.cache(c.origin, state); err != nil {
		return state, &CacheError{Err: err}
	}
	return state, nil
}
func (c *SocialClient) Register(ctx context.Context, label string) (SocialState, error) {
	if !textOK(label, 128, false) {
		return SocialState{}, ErrInvalid
	}
	return c.call(ctx, "register", socialRequest{Label: label})
}
func (c *SocialClient) State(ctx context.Context) (SocialState, error) {
	return c.call(ctx, "state", socialRequest{})
}
func (c *SocialClient) Request(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "request", socialRequest{AccountID: id})
}
func (c *SocialClient) Accept(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "accept", socialRequest{AccountID: id})
}
func (c *SocialClient) Decline(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "decline", socialRequest{AccountID: id})
}
func (c *SocialClient) Cancel(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "cancel", socialRequest{AccountID: id})
}
func (c *SocialClient) Remove(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "remove", socialRequest{AccountID: id})
}
func (c *SocialClient) Block(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "block", socialRequest{AccountID: id})
}
func (c *SocialClient) Unblock(ctx context.Context, id string) (SocialState, error) {
	return c.call(ctx, "unblock", socialRequest{AccountID: id})
}
