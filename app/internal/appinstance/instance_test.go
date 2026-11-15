package appinstance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func privateConfig(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private-config")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "re-psn.ini")
}
func instancePair(t *testing.T) (*Instance, *Instance) {
	t.Helper()
	config := privateConfig(t)
	owner, yes, err := Acquire(config)
	if err != nil || !yes {
		t.Fatal("owner acquisition", yes, err)
	}
	t.Cleanup(owner.Close)
	peer, yes, err := Acquire(config)
	if err != nil || yes {
		t.Fatal("second instance acquired ownership", yes, err)
	}
	t.Cleanup(peer.Close)
	return owner, peer
}
func fixtureMAC(key, domain, value string) string {
	h := hmac.New(sha256.New, []byte(key))
	_, _ = io.WriteString(h, "re-psn-ui-"+domain+"-v1\n")
	_, _ = io.WriteString(h, value)
	return hex.EncodeToString(h.Sum(nil))
}

func TestInstanceAliasesShareOneOwner(t *testing.T) {
	config := privateConfig(t)
	owner, yes, err := Acquire(Canonical(config))
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	defer owner.Close()
	dir := filepath.Dir(config)
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := dir + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(config)
	canonical := Canonical(alias)
	if canonical != Canonical(config) {
		t.Fatal("ancestor alias did not resolve")
	}
	peer, yes, err := Acquire(canonical)
	if err != nil || yes {
		t.Fatal("ancestor alias bypassed lock", yes, err)
	}
	peer.Close()
	if runtime.GOOS == "windows" {
		peer, yes, err = Acquire(filepath.Join(dir, "RE-PSN.INI"))
		if err != nil || yes {
			t.Fatal("Windows basename case bypassed lock", yes, err)
		}
		peer.Close()
	}
	t.Run("symlink ancestor", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(dir, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skip("symlink creation requires platform privileges; case/ancestor alias controls still run")
			}
			t.Fatal(err)
		}
		resolved := Canonical(filepath.Join(link, filepath.Base(config)))
		if resolved != Canonical(config) {
			t.Fatal("symlink alias not canonical")
		}
		peer, yes, err := Acquire(resolved)
		if err != nil || yes {
			t.Fatal("symlink alias bypassed lock", yes, err)
		}
		peer.Close()
		future := Canonical(filepath.Join(link, "future", "settings.ini"))
		parent, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(parent, "future", "settings.ini"); future != want {
			t.Fatalf("missing descendant lost canonical ancestor: got %q, want %q", future, want)
		}
	})
}

func TestInstanceCloseRemovesOwnLatestPublication(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
	}{{"first", 1}, {"replacement", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			owner, yes, err := Acquire(privateConfig(t))
			if err != nil || !yes {
				t.Fatal(yes, err)
			}
			defer owner.Close()
			for n := 0; n < tc.count; n++ {
				if err := owner.Publish(Locator{Profile: "synthetic"}); err != nil {
					t.Fatal(err)
				}
			}
			owner.Close()
			if _, err := os.Lstat(owner.path); !os.IsNotExist(err) {
				t.Fatalf("owned publication remains after Close: %v", err)
			}
		})
	}
}

func TestInstanceClosePreservesIdenticalReplacement(t *testing.T) {
	owner, yes, err := Acquire(privateConfig(t))
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	defer owner.Close()
	if err := owner.Publish(Locator{Profile: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(owner.path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(filepath.Dir(owner.path), "replacement-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replace(f.Name(), owner.path); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	saved, err := os.ReadFile(owner.path)
	if err != nil || string(saved) != string(raw) {
		t.Fatalf("Close removed identical-content foreign publication: %v", err)
	}
}

func TestInstancePrivatePublicationAndReplacementOwnership(t *testing.T) {
	config := privateConfig(t)
	owner, yes, err := Acquire(config)
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	defer owner.Close()
	value := Locator{Origin: "http://127.0.0.1:12345", Key: strings.Repeat("a", 64), Profile: "synthetic-profile", Binary: "synthetic-server"}
	if err := owner.Publish(value); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{owner.path, owner.file.Name()} {
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
			t.Fatal("instance artifact not private regular file", err)
		}
	}
	if err := owner.Publish(Locator{Origin: value.Origin, Key: strings.Repeat("b", 64), Profile: value.Profile, Binary: value.Binary}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(owner.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved Locator
	if json.Unmarshal(raw, &saved) != nil || saved.Key != strings.Repeat("b", 64) {
		t.Fatal("publication replacement failed")
	}
	// Simulate a later writer's atomic replacement, not an in-place edit of our inode.
	f, err := os.CreateTemp(filepath.Dir(owner.path), "replacement-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("synthetic later marker")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replace(f.Name(), owner.path); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	raw, err = os.ReadFile(owner.path)
	if err != nil || string(raw) != "synthetic later marker" {
		t.Fatal("Close removed another publication", err)
	}
	newOwner, yes, err := Acquire(config)
	if err != nil || !yes {
		t.Fatal("Close did not release lock", yes, err)
	}
	newOwner.Close()
	if _, err := os.Stat(owner.path); err != nil {
		t.Fatal("unpublished owner removed unrelated locator", err)
	}
}

func TestInstanceRejectsNonPrivateLocatorBeforeNetwork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission policy; Windows ACL privacy is a separate platform qualification")
	}
	owner, peer := instancePair(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) }))
	defer server.Close()
	if err := owner.Publish(Locator{Origin: server.URL, Key: strings.Repeat("c", 64), Profile: "profile", Binary: "server"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(owner.path, 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := peer.Reopen(ctx, "profile", "server"); err == nil {
		t.Fatal("world-readable locator accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("private-file validation happened after network")
	}
	dir := filepath.Join(t.TempDir(), "public-config")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if i, yes, err := Acquire(filepath.Join(dir, "settings.ini")); err == nil || yes {
		if i != nil {
			i.Close()
		}
		t.Fatal("nonprivate settings directory accepted")
	}
}

func TestInstanceReopenAuthenticatesResponseOriginAndChallenge(t *testing.T) {
	for _, mode := range []string{"valid", "foreign proof", "wrong domain", "wrong challenge", "foreign origin", "query URL", "bad fragment", "oversize", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			owner, peer := instancePair(t)
			key := strings.Repeat("d", 64)
			var calls, foreignCalls atomic.Int32
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				foreignCalls.Add(1)
				http.Error(w, "must not follow", 500)
			}))
			defer foreign.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/reopen" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
					t.Error("unexpected reopen request framing")
					http.Error(w, "bad request", 400)
					return
				}
				var request struct {
					Challenge string `json:"challenge"`
					Proof     string `json:"proof"`
				}
				d := json.NewDecoder(io.LimitReader(r.Body, 1024))
				d.DisallowUnknownFields()
				if err := d.Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				nonce, err := hex.DecodeString(request.Challenge)
				if err != nil || len(nonce) != 32 || !Equal(request.Proof, fixtureMAC(key, "request", request.Challenge)) {
					t.Error("request HMAC/domain invalid")
					http.Error(w, "unauthorized", 401)
					return
				}
				link := "http://" + r.Host + "/#token=" + strings.Repeat("e", 64)
				challenge, domain, signingKey := request.Challenge, "response", key
				switch mode {
				case "foreign proof":
					signingKey = strings.Repeat("f", 64)
				case "wrong domain":
					domain = "request"
				case "wrong challenge":
					challenge = strings.Repeat("0", 64)
				case "foreign origin":
					link = foreign.URL + "/#token=" + strings.Repeat("e", 64)
				case "query URL":
					link = "http://" + r.Host + "/?token=synthetic#token=" + strings.Repeat("e", 64)
				case "bad fragment":
					link = "http://" + r.Host + "/#token=short"
				case "oversize":
					_, _ = io.WriteString(w, strings.Repeat(" ", 2049))
					return
				case "redirect":
					http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"url": link, "proof": fixtureMAC(signingKey, domain, challenge+"\n"+link)})
			}))
			defer server.Close()
			if err := owner.Publish(Locator{Origin: server.URL, Key: key, Profile: "synthetic-profile", Binary: "synthetic-server"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			link, err := peer.Reopen(ctx, "synthetic-profile", "synthetic-server")
			if mode == "valid" {
				if err != nil || !ValidLink(link, server.URL) {
					t.Fatal("valid authenticated control failed", err)
				}
			} else if err == nil || link != "" {
				t.Fatal("untrusted reopen response accepted", mode)
			}
			if calls.Load() != 1 || foreignCalls.Load() != 0 {
				t.Fatal("request count/redirect boundary", calls.Load(), foreignCalls.Load())
			}
		})
	}
}

func TestInstanceExplicitSettingsMismatchDoesNotContactOwner(t *testing.T) {
	owner, peer := instancePair(t)
	key := strings.Repeat("a", 64)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "not needed", 500) }))
	defer server.Close()
	if err := owner.Publish(Locator{Origin: server.URL, Key: key, Profile: "original-profile", Binary: "original-server"}); err != nil {
		t.Fatal(err)
	}
	for _, settings := range [][2]string{{"different-profile", "original-server"}, {"original-profile", "different-server"}} {
		if _, err := peer.Reopen(context.Background(), settings[0], settings[1]); err == nil || !strings.Contains(err.Error(), "different launch settings") {
			t.Fatal("settings mismatch was ignored", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("mismatch was checked after network")
	}
}

func TestInstanceProofDomainsAndAllowedLinks(t *testing.T) {
	key := strings.Repeat("b", 64)
	request := Proof(key, "request", "synthetic-nonce")
	response := Proof(key, "response", "synthetic-nonce")
	if Equal(request, response) || !Equal(request, fixtureMAC(key, "request", "synthetic-nonce")) {
		t.Fatal("proof domain/framing mismatch")
	}
	for _, bad := range []string{"", strings.Repeat("0", 62), strings.Repeat("z", 64), request + "00"} {
		if Equal(request, bad) {
			t.Fatal("malformed MAC accepted")
		}
	}
	origin := "http://127.0.0.1:12345"
	good := origin + "/#token=" + strings.Repeat("c", 64)
	if !ValidLink(good, origin) {
		t.Fatal("valid loopback launch URL rejected")
	}
	for _, bad := range []string{"https://127.0.0.1:12345/#token=" + strings.Repeat("c", 64), "http://localhost:12345/#token=" + strings.Repeat("c", 64), origin + "/other#token=" + strings.Repeat("c", 64), origin + "/?secret=value#token=" + strings.Repeat("c", 64), origin + "/#token=short", "http://user@127.0.0.1:12345/#token=" + strings.Repeat("c", 64)} {
		if ValidLink(bad, origin) {
			t.Fatal("foreign or malformed link accepted")
		}
	}
}
