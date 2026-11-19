package main

// Generator for testdata/pass_vectors.jsonl. The file is checked in and is what other implementations read; this code
// only exists so that the passes in it can be made again, and TestPassVectorsFileIsCurrent fails when the two differ.
// Run "PASS_VECTORS_UPDATE=1 go test -run TestPassVectorsFileIsCurrent ./server" to rewrite the file after changing the
// table. Every expectation below is written by hand; none is computed by the verifier it is used against.
//
// The signing key is TEST-ONLY. Its seed is the SHA-256 of a public sentence (testVectorSeedText), so anyone can make
// it again, and it has never signed anything but these vectors. Do not put it in a key file that a real server reads.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	testVectorSeedText = "re-psn sign-in pass test vectors: TEST ONLY, not a secret"
	vecBase            = int64(1800000000) // the iat of the base pass, in seconds
	vecFile            = "testdata/pass_vectors.jsonl"
)

type vecMeta struct {
	Format        string `json:"format"`
	TestOnly      string `json:"test_only"`
	KeyDerivation string `json:"key_derivation"`
	Kid           string `json:"kid"`
	PublicKey     string `json:"public_key"`
	Issuer        string `json:"issuer"`
	Audience      string `json:"audience"`
	Fields        string `json:"fields"`
}

type vector struct {
	Name     string   `json:"name"`
	Note     string   `json:"note,omitempty"`
	Pass     string   `json:"pass"`
	Now      int64    `json:"now"`
	Audience string   `json:"audience,omitempty"` // replaces the meta audience for this vector
	Accept   bool     `json:"accept"`             // what the Go server does today
	Strict   *bool    `json:"strict,omitempty"`   // what a strict verifier does, when that differs
	Reason   string   `json:"reason,omitempty"`   // the Go server's log reason for a refusal
	Sub      string   `json:"sub,omitempty"`
	Scope    []string `json:"scope,omitempty"`
	Exp      int64    `json:"exp,omitempty"`
}

func vecKey() (ed25519.PrivateKey, ed25519.PublicKey) {
	h := sha256.Sum256([]byte(testVectorSeedText))
	priv := ed25519.NewKeyFromSeed(h[:])
	return priv, priv.Public().(ed25519.PublicKey)
}

func vecMetaLine() vecMeta {
	_, pub := vecKey()
	return vecMeta{
		Format:        "re-psn sign-in pass vectors 1",
		TestOnly:      "The key below is a throwaway made for these tests. It protects nothing and no real server or login service may trust it.",
		KeyDerivation: "private seed = SHA-256 of the ASCII text \"" + testVectorSeedText + "\"; Ed25519 as in RFC 8032",
		Kid:           "k1",
		PublicKey:     b64.EncodeToString(pub),
		Issuer:        testIssuer,
		Audience:      testAud,
		Fields:        "pass and now (unix seconds) are the input. accept is what the Go server does today; strict, when present, is what a strict verifier must do instead (see docs/api.md). reason is the Go server's log text for a refusal and is informative only. sub, scope and exp are the claims of an accepted pass. audience replaces the meta audience for one vector.",
	}
}

// Fields of the base payload, in the order the login service writes them.
var vecFields = []string{
	`"v":"v1"`, `"kid":"k1"`, `"iss":"login.test"`, `"aud":"server.test"`, `"sub":"github:123"`,
	`"scope":["relay"]`, `"iat":1800000000`, `"exp":1800003600`,
}

const (
	fV = iota
	fKid
	fIss
	fAud
	fSub
	fScope
	fIat
	fExp
)

// obj joins fields into a JSON object text.
func obj(f []string) string { return "{" + strings.Join(f, ",") + "}" }

// with returns the base fields with field i replaced.
func with(i int, s string) []string {
	f := append([]string(nil), vecFields...)
	f[i] = s
	return f
}

// without returns the base fields with field i left out.
func without(i int) []string {
	f := append([]string(nil), vecFields...)
	return append(f[:i], f[i+1:]...)
}

func sign(head string) string {
	priv, _ := vecKey()
	return b64.EncodeToString(ed25519.Sign(priv, []byte(head)))
}

// mk signs payload the way the login service does.
func mk(payload string) string {
	head := "v1." + b64.EncodeToString([]byte(payload))
	return head + "." + sign(head)
}

func yes(n string, p string, now int64, sub string, scope []string, exp int64, note string) vector {
	return vector{Name: n, Note: note, Pass: p, Now: now, Accept: true, Sub: sub, Scope: scope, Exp: exp}
}

func no(n string, p string, now int64, reason, note string) vector {
	return vector{Name: n, Note: note, Pass: p, Now: now, Reason: reason}
}

// lenient is a pass the Go server accepts and a strict verifier refuses.
func lenient(n string, payload string, note string) vector {
	f := false
	return vector{Name: n, Note: note, Pass: mk(payload), Now: vecBase, Accept: true, Strict: &f,
		Sub: "github:123", Scope: []string{"relay"}, Exp: vecBase + 3600}
}

// longAud returns an audience of n bytes and a pass for it.
func longAudPass(n int) (aud, pass string) {
	aud = "server." + strings.Repeat("a", n-7)
	return aud, mk(obj(with(fAud, `"aud":"`+aud+`"`)))
}

// longAudOfLen finds the audience for which the whole pass is exactly total bytes.
func longAudOfLen(total int) (aud, pass string) {
	for n := 8; n < 3000; n++ {
		a, p := longAudPass(n)
		if len(p) == total {
			return a, p
		}
	}
	panic(fmt.Sprintf("no pass of %d bytes", total))
}

func buildVectors() []vector {
	var v []vector
	base := mk(obj(vecFields))
	relay := []string{"relay"}
	good := func(n, note string, f []string, scope []string) {
		v = append(v, yes(n, mk(obj(f)), vecBase, "github:123", scope, vecBase+3600, note))
	}

	// Valid passes.
	v = append(v, yes("valid-relay", base, vecBase, "github:123", relay, vecBase+3600, "the base pass: every other vector is this one with one change"))
	good("valid-connect", "", with(fScope, `"scope":["connect"]`), []string{"connect"})
	good("valid-connect-relay", "", with(fScope, `"scope":["connect","relay"]`), []string{"connect", "relay"})
	good("valid-relay-connect-order", "scope order is not significant", with(fScope, `"scope":["relay","connect"]`), []string{"relay", "connect"})
	v = append(v, yes("valid-discord", mk(obj(with(fSub, `"sub":"discord:456"`))), vecBase, "discord:456", relay, vecBase+3600, ""))
	v = append(v, yes("valid-sub-19-digits", mk(obj(with(fSub, `"sub":"discord:9999999999999999999"`))), vecBase, "discord:9999999999999999999", relay, vecBase+3600, "19 digits is the most; the number is not parsed"))
	good("valid-field-order", "JSON objects are unordered", []string{vecFields[fExp], vecFields[fIat], vecFields[fScope], vecFields[fSub], vecFields[fAud], vecFields[fIss], vecFields[fKid], vecFields[fV]}, relay)
	v = append(v, yes("valid-whitespace", mk(`{"v":"v1", "kid":"k1", "iss":"login.test", "aud":"server.test", "sub":"github:123", "scope":["relay"], "iat":1800000000, "exp":1800003600}`+"\n"), vecBase, "github:123", relay, vecBase+3600, "whitespace between tokens is JSON whitespace"))
	v = append(v, yes("valid-escaped-key", mk(`{"\u0076":"v1","kid":"k1","iss":"login.test","aud":"server.test","sub":"github:123","scope":["relay"],"iat":1800000000,"exp":1800003600}`), vecBase, "github:123", relay, vecBase+3600, "a key spelled with an escape is the same key once the string is decoded"))
	good("valid-life-1s", "", with(fExp, `"exp":1800000001`), relay)
	v[len(v)-1].Exp = vecBase + 1

	// Time. The pass has iat = now0 and exp = now0 + 3600; 60 s of skew on both ends.
	v = append(v, yes("time-at-iat", base, vecBase, "github:123", relay, vecBase+3600, ""))
	v = append(v, yes("time-60s-before-iat", base, vecBase-60, "github:123", relay, vecBase+3600, "skew edge: still accepted"))
	v = append(v, no("time-61s-before-iat", base, vecBase-61, "not yet valid", "skew edge: refused"))
	v = append(v, yes("time-at-exp", base, vecBase+3600, "github:123", relay, vecBase+3600, ""))
	v = append(v, yes("time-59s-after-exp", base, vecBase+3659, "github:123", relay, vecBase+3600, "skew edge: still accepted"))
	v = append(v, no("time-60s-after-exp", base, vecBase+3660, "expired", "skew edge: refused"))
	v = append(v, no("time-far-after-exp", base, vecBase+1<<40, "expired", ""))

	// Lifetime and numbers.
	v = append(v, no("life-3601s", mk(obj(with(fExp, `"exp":1800003601`))), vecBase, "lifetime", "one second over the hour"))
	v = append(v, no("life-exp-equals-iat", mk(obj(with(fExp, `"exp":1800000000`))), vecBase, "lifetime", ""))
	v = append(v, no("life-exp-before-iat", mk(obj(with(fExp, `"exp":1799999999`))), vecBase, "lifetime", ""))
	v = append(v, no("life-iat-negative", mk(obj(with(fIat, `"iat":-1`))), vecBase, "lifetime", ""))
	v = append(v, no("life-iat-too-large", mk(obj(with(fIat, `"iat":1099511627777`))), vecBase, "lifetime", "iat above 2^40"))
	v = append(v, no("num-iat-float", mk(obj(with(fIat, `"iat":1800000000.0`))), vecBase, "payload", "integers only: 1800000000.0 is not a whole-second number"))
	v = append(v, no("num-exp-exponent", mk(obj(with(fExp, `"exp":1.8000036e9`))), vecBase, "payload", "integers only"))
	v = append(v, no("num-exp-string", mk(obj(with(fExp, `"exp":"1800003600"`))), vecBase, "payload", ""))
	v = append(v, no("num-iat-overflow", mk(obj(with(fIat, `"iat":9223372036854775808`))), vecBase, "payload", "does not fit 64 bits"))
	v = append(v, no("num-exp-leading-zero", mk(obj(with(fExp, `"exp":01800003600`))), vecBase, "payload", "not valid JSON"))

	// Fields.
	v = append(v, no("field-unknown", mk(obj(append([]string{`"x":1`}, vecFields...))), vecBase, "payload", "an unknown field refuses the pass"))
	v = append(v, no("field-cnf-object", mk(obj(append(append([]string(nil), vecFields...), `"cnf":{"jkt":"x"}`))), vecBase, "bound pass on a bearer-only server", "cnf marks a pass bound to a client key"))
	v = append(v, no("field-cnf-null", mk(obj(append(append([]string(nil), vecFields...), `"cnf":null`))), vecBase, "bound pass on a bearer-only server", ""))
	v = append(v, no("field-cnf-empty", mk(obj(append(append([]string(nil), vecFields...), `"cnf":{}`))), vecBase, "bound pass on a bearer-only server", ""))
	for i, n := range []string{"v", "kid", "iss", "aud", "sub", "scope", "iat", "exp"} {
		why := map[string]string{"v": "key or version", "kid": "key or version", "iss": "issuer", "aud": "audience", "sub": "subject", "scope": "scope", "iat": "lifetime", "exp": "lifetime"}[n]
		v = append(v, no("field-missing-"+n, mk(obj(without(i))), vecBase, why, "a missing field refuses the pass"))
	}
	// A JSON null is read by encoding/json as a missing value, so the field takes its zero value rather than refusing the pass.
	for i, n := range []string{"v", "kid", "iss", "aud", "sub", "scope", "iat", "exp"} {
		why := map[string]string{"v": "key or version", "kid": "key or version", "iss": "issuer", "aud": "audience", "sub": "subject", "scope": "scope", "iat": "lifetime", "exp": "lifetime"}[n]
		v = append(v, no("field-null-"+n, mk(obj(with(i, `"`+n+`":null`))), vecBase, why, "null is read as a missing value: the field is empty or 0, and the check that follows refuses it"))
	}
	{
		f := with(fIat, `"iat":null`)
		f[fExp] = `"exp":3600`
		f2 := false
		v = append(v, vector{Name: "lenient-null-iat", Note: "null is read as 0, so this pass is valid at second 1000 with iat 0; a strict verifier refuses a null",
			Pass: mk(obj(f)), Now: 1000, Accept: true, Strict: &f2, Sub: "github:123", Scope: []string{"relay"}, Exp: 3600})
		g := without(fIat)
		for i, x := range g {
			if x == vecFields[fExp] {
				g[i] = `"exp":3600`
			}
		}
		v = append(v, vector{Name: "lenient-missing-iat", Note: "a missing iat is read as 0, so this pass is valid at second 1000; a strict verifier refuses a missing field",
			Pass: mk(obj(g)), Now: 1000, Accept: true, Strict: &f2, Sub: "github:123", Scope: []string{"relay"}, Exp: 3600})
	}
	v = append(v, no("field-v-v2", mk(obj(with(fV, `"v":"v2"`))), vecBase, "key or version", ""))
	v = append(v, no("field-kid-unknown", mk(obj(with(fKid, `"kid":"k9"`))), vecBase, "key or version", "signed by the right key, but the kid is not in the key file"))
	v = append(v, no("field-kid-case", mk(obj(with(fKid, `"kid":"K1"`))), vecBase, "key or version", "values are compared exactly"))
	v = append(v, no("field-iss-wrong", mk(obj(with(fIss, `"iss":"other.test"`))), vecBase, "issuer", ""))
	v = append(v, no("field-aud-wrong", mk(obj(with(fAud, `"aud":"other.test"`))), vecBase, "audience", "made for another server"))
	v = append(v, no("field-aud-case", mk(obj(with(fAud, `"aud":"Server.test"`))), vecBase, "audience", "values are compared exactly"))
	v = append(v, no("field-aud-prefix", mk(obj(with(fAud, `"aud":"server.test "`))), vecBase, "audience", "no trimming"))

	// Subjects.
	for _, s := range []struct{ n, sub string }{
		{"sub-leading-zero", "github:0123"}, {"sub-zero", "github:0"}, {"sub-20-digits", "github:12345678901234567890"},
		{"sub-empty-number", "github:"}, {"sub-plus", "github:+1"}, {"sub-space", "github: 1"}, {"sub-other-provider", "twitter:1"},
		{"sub-case-provider", "GitHub:1"}, {"sub-no-colon", "github1"}, {"sub-name", "github:octocat"},
	} {
		v = append(v, no(s.n, mk(obj(with(fSub, `"sub":"`+s.sub+`"`))), vecBase, "subject", "one spelling per account"))
	}

	// Scope forms.
	for _, s := range []struct{ n, scope, why string }{
		{"scope-empty", `[]`, "scope"}, {"scope-null", `null`, "scope"}, {"scope-unknown", `["admin"]`, "scope"},
		{"scope-case", `["Relay"]`, "scope"}, {"scope-repeat", `["relay","relay"]`, "scope"},
		{"scope-repeat-apart", `["relay","connect","relay"]`, "scope"}, {"scope-one-unknown", `["relay","admin"]`, "scope"},
		{"scope-string", `"relay"`, "payload"}, {"scope-number", `1`, "payload"}, {"scope-nested", `[["relay"]]`, "payload"},
		{"scope-comma-list", `["relay,connect"]`, "scope"},
	} {
		v = append(v, no(s.n, mk(obj(with(fScope, `"scope":`+s.scope))), vecBase, s.why, ""))
	}

	// Payload shape.
	for _, s := range []struct{ n, payload, why, note string }{
		{"json-empty", ``, "payload", ""}, {"json-null", `null`, "key or version", "decodes to nothing, so no key is named"},
		{"json-array", `[` + obj(vecFields) + `]`, "payload", ""}, {"json-empty-object", `{}`, "key or version", ""},
		{"json-trailing-comma", strings.TrimSuffix(obj(vecFields), "}") + `,}`, "payload", ""},
		{"json-trailing-garbage", obj(vecFields) + `x`, "payload", ""},
		{"json-two-objects", obj(vecFields) + obj(vecFields), "payload", "only one value is allowed"},
		{"json-bom", "\xef\xbb\xbf" + obj(vecFields), "payload", ""},
		{"json-control-char", strings.Replace(obj(vecFields), "server.test", "server\x01test", 1), "payload", ""},
		{"json-truncated", obj(vecFields)[:40], "payload", ""},
	} {
		v = append(v, no(s.n, mk(s.payload), vecBase, s.why, s.note))
	}

	// Payloads where the Go server and a strict reader disagree: case variants and repeated keys (lenient-null-iat is above).
	v = append(v,
		lenient("lenient-case-keys", `{"V":"v1","kid":"k1","iss":"login.test","aud":"server.test","Sub":"github:123","scope":["relay"],"iat":1800000000,"exp":1800003600}`, "the Go decoder matches key names without regard to case"),
		lenient("lenient-case-aud-last", `{"v":"v1","kid":"k1","iss":"login.test","aud":"evil","AUD":"server.test","sub":"github:123","scope":["relay"],"iat":1800000000,"exp":1800003600}`, "a case variant counts as the same key, so the last one wins"),
		lenient("lenient-case-aud-only", `{"v":"v1","kid":"k1","iss":"login.test","auD":"server.test","sub":"github:123","scope":["relay"],"iat":1800000000,"exp":1800003600}`, "a key that is not spelled aud is taken for aud"),
		lenient("lenient-dup-aud-last-good", `{"v":"v1","kid":"k1","iss":"login.test","aud":"evil","aud":"server.test","sub":"github:123","scope":["relay"],"iat":1800000000,"exp":1800003600}`, "the last duplicate wins: the first aud is ignored"),
		lenient("lenient-dup-exp", `{"v":"v1","kid":"k1","iss":"login.test","aud":"server.test","sub":"github:123","scope":["relay"],"exp":1800099999,"iat":1800000000,"exp":1800003600}`, "the last duplicate wins: the first exp, which would break the lifetime rule, is ignored"),
	)
	v = append(v, no("dup-aud-last-evil", mk(`{"v":"v1","kid":"k1","iss":"login.test","aud":"server.test","aud":"evil","sub":"github:123","scope":["relay"],"iat":1800000000,"exp":1800003600}`), vecBase, "audience", "the last duplicate wins here too, and it is the wrong one; a strict verifier refuses it for the duplicate"))
	v = append(v, no("dup-sub-last-evil", mk(`{"v":"v1","kid":"k1","iss":"login.test","aud":"server.test","sub":"github:123","sub":"github:0","scope":["relay"],"iat":1800000000,"exp":1800003600}`), vecBase, "subject", ""))

	// Encoding of the three parts.
	head := strings.Split(base, ".")
	v = append(v,
		no("enc-no-prefix", head[1]+"."+head[2], vecBase, "format", ""),
		no("enc-prefix-v2", "v2."+head[1]+"."+head[2], vecBase, "format", ""),
		no("enc-prefix-upper", "V1."+head[1]+"."+head[2], vecBase, "format", ""),
		no("enc-four-parts", base+".", vecBase, "format", ""),
		no("enc-three-parts-empty-sig", head[0]+"."+head[1]+".", vecBase, "format", ""),
		no("enc-empty", "", vecBase, "format", ""),
		no("enc-leading-space", " "+base, vecBase, "format", ""),
		no("enc-trailing-newline", base+"\n", vecBase, "format", "one spelling only"),
		no("enc-newline-inside", head[0]+"."+head[1][:20]+"\n"+head[1][20:]+"."+head[2], vecBase, "format", "the base64 decoder of some languages skips line breaks"),
		no("enc-sig-padded", base+"==", vecBase, "format", "base64url without padding"),
	)
	// A padded payload part, signed as sent: only the padding is wrong.
	for pad := 0; ; pad++ {
		payload := obj(with(fSub, `"sub":"github:`+strings.Repeat("1", pad+1)+`"`))
		if len(payload)%3 == 0 {
			continue
		}
		enc := b64.EncodeToString([]byte(payload))
		for len(enc)%4 != 0 {
			enc += "="
		}
		h := "v1." + enc
		v = append(v, no("enc-payload-padded", h+"."+sign(h), vecBase, "format", "signed over the padded text as sent, so only the padding refuses it"))
		break
	}
	// A payload part whose last character has unused bits set, signed as sent: the same bytes as a valid payload, spelled another way.
	for pad := 0; ; pad++ {
		payload := obj(with(fSub, `"sub":"github:`+strings.Repeat("1", pad+1)+`"`))
		if len(payload)%3 == 0 {
			continue
		}
		enc := b64.EncodeToString([]byte(payload))
		const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
		h := "v1." + enc[:len(enc)-1] + string(alpha[strings.IndexByte(alpha, enc[len(enc)-1])^1])
		v = append(v, no("enc-payload-noncanonical-tail", h+"."+sign(h), vecBase, "format", "unused bits set in the last character of the payload part, signed as sent"))
		break
	}
	// A signature in the standard alphabet (+ and / for - and _).
	for iat := vecBase; ; iat++ {
		f := with(fIat, fmt.Sprintf(`"iat":%d`, iat))
		f[fExp] = fmt.Sprintf(`"exp":%d`, iat+3600)
		p := mk(obj(f))
		sig := p[strings.LastIndex(p, ".")+1:]
		if strings.ContainsAny(sig, "-_") {
			std := strings.NewReplacer("-", "+", "_", "/").Replace(sig)
			v = append(v, no("enc-sig-standard-alphabet", p[:len(p)-len(sig)]+std, iat, "format", "base64url, not base64"))
			v = append(v, yes("enc-sig-urlsafe-control", p, iat, "github:123", relay, iat+3600, "the same pass with the url-safe alphabet"))
			break
		}
	}
	// The last character of a 64-byte signature holds four unused bits, which must be zero.
	sig := head[2]
	last := strings.IndexByte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", sig[len(sig)-1])
	alt := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"[last^1]
	v = append(v, no("enc-sig-noncanonical-tail", head[0]+"."+head[1]+"."+sig[:len(sig)-1]+string(alt), vecBase, "format", "unused bits set: one signature, one spelling"))
	// Payload part with the same property is covered when its length is not a multiple of 3; see enc-payload-padded.

	// Signature.
	priv2 := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	hd := head[0] + "." + head[1]
	v = append(v,
		no("sig-other-key", hd+"."+b64.EncodeToString(ed25519.Sign(priv2, []byte(hd))), vecBase, "signature", "a different throwaway key"),
		no("sig-zero", hd+"."+b64.EncodeToString(make([]byte, 64)), vecBase, "signature", ""),
		no("sig-payload-only", hd+"."+b64.EncodeToString(ed25519.Sign(func() ed25519.PrivateKey { k, _ := vecKey(); return k }(), []byte(obj(vecFields)))), vecBase, "signature", "the signature covers the text of the first two parts, not the decoded payload"),
		no("sig-63-bytes", hd+"."+b64.EncodeToString(make([]byte, 63)), vecBase, "format", ""),
		no("sig-65-bytes", hd+"."+b64.EncodeToString(make([]byte, 65)), vecBase, "format", ""),
		no("sig-payload-changed", "v1."+b64.EncodeToString([]byte(obj(with(fAud, `"aud":"server.tesu"`))))+"."+head[2], vecBase, "signature", "payload edited after signing"),
	)
	flip := []byte(head[2])
	if flip[3] == 'A' {
		flip[3] = 'B'
	} else {
		flip[3] = 'A'
	}
	v = append(v, no("sig-bit-flipped", hd+"."+string(flip), vecBase, "signature", ""))

	// Size: 2048 bytes is the most, counting the whole pass text. A long audience makes it without whitespace.
	a2048, p2048 := longAudOfLen(2048)
	a2049, p2049 := longAudOfLen(2049)
	e := yes("size-2048", p2048, vecBase, "github:123", relay, vecBase+3600, "exactly 2048 bytes of pass, made valid with a long audience")
	e.Audience = a2048
	v = append(v, e)
	n := no("size-2049", p2049, vecBase, "too long", "one byte over; the size check comes before any decoding")
	n.Audience = a2049
	v = append(v, n)
	v = append(v, no("size-4096-junk", strings.Repeat("A", 4096), vecBase, "too long", ""))
	return v
}

func vectorFileBytes() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"meta": vecMetaLine()}); err != nil {
		panic(err)
	}
	for _, v := range buildVectors() {
		if err := enc.Encode(v); err != nil {
			panic(err)
		}
	}
	return buf.Bytes()
}

func TestPassVectorsFileIsCurrent(t *testing.T) {
	want := vectorFileBytes()
	if os.Getenv("PASS_VECTORS_UPDATE") != "" {
		if err := os.WriteFile(vecFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(vecFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not what the generator makes; run PASS_VECTORS_UPDATE=1 go test -run TestPassVectorsFileIsCurrent ./server and review the diff", vecFile)
	}
}
