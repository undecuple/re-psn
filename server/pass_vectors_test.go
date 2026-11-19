package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestPassVectors runs every line of testdata/pass_vectors.jsonl against the server's verifier. The file is data for any
// implementation of the pass format: the key in it is a throwaway for tests, and the Go server's answer is "accept". A
// strict verifier reads "strict" where a line has one. The file is checked in; pass_vectors_gen_test.go can make it again.
func TestPassVectors(t *testing.T) {
	f, err := os.Open(vecFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if !sc.Scan() {
		t.Fatal("empty vector file")
	}
	var head struct {
		Meta vecMeta `json:"meta"`
	}
	if err := json.Unmarshal(sc.Bytes(), &head); err != nil || head.Meta.Kid == "" {
		t.Fatalf("first line is not the meta line: %v", err)
	}
	m := head.Meta
	raw, err := b64.DecodeString(m.PublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		t.Fatalf("bad public key in meta: %v", err)
	}
	if !strings.Contains(m.TestOnly, "throwaway") {
		t.Fatal("the meta line must say the key is a throwaway")
	}
	cfg := &passConfig{keys: map[string]ed25519.PublicKey{m.Kid: raw}, revoked: map[string]bool{}, issuer: m.Issuer, aud: m.Audience}
	n, accepted, refused, lenientN := 0, 0, 0, 0
	seen := map[string]bool{}
	for sc.Scan() {
		var v vector
		dec := json.NewDecoder(strings.NewReader(sc.Text()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("line %d: %v", n+2, err)
		}
		n++
		if v.Name == "" || seen[v.Name] {
			t.Fatalf("line %d: name %q is empty or repeated", n+1, v.Name)
		}
		seen[v.Name] = true
		if v.Strict != nil && (!v.Accept || *v.Strict) {
			t.Errorf("%s: strict is only for a pass the Go server accepts and a strict verifier refuses", v.Name)
		}
		c := cfg
		if v.Audience != "" {
			cc := *cfg
			cc.aud = v.Audience
			c = &cc
		}
		cl, why, err := c.checkPass([]byte(v.Pass), time.Unix(v.Now, 0))
		if (err == nil) != v.Accept {
			t.Errorf("%s: accepted=%v (reason %q), want accept=%v", v.Name, err == nil, why, v.Accept)
			continue
		}
		if v.Accept {
			accepted++
			var scope []string
			if cl.scopes&scopeConnect != 0 {
				scope = append(scope, "connect")
			}
			if cl.scopes&scopeRelay != 0 {
				scope = append(scope, "relay")
			}
			want := append([]string(nil), v.Scope...)
			if len(want) == 2 && want[0] == "relay" { // the bits do not keep the order
				want[0], want[1] = want[1], want[0]
			}
			if cl.sub != v.Sub || cl.exp != v.Exp || !reflect.DeepEqual(scope, want) || cl.kid != m.Kid {
				t.Errorf("%s: claims %+v, want sub %s scope %v exp %d", v.Name, cl, v.Sub, v.Scope, v.Exp)
			}
			if v.Strict != nil {
				lenientN++
			}
		} else {
			refused++
			if why != v.Reason {
				t.Errorf("%s: refused for %q, want %q", v.Name, why, v.Reason)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// A file that lost most of its lines would still pass line by line, so the shape is pinned.
	if n < 100 || accepted < 15 || refused < 80 || lenientN != 7 {
		t.Fatalf("vector file has %d vectors (%d accepted, %d refused, %d lenient); expected at least 100, 15, 80 and exactly 7", n, accepted, refused, lenientN)
	}
}
