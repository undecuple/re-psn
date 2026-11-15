package desktop

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBrowserLaunchFileProtectsBootstrap(t *testing.T) {
	target := "http://127.0.0.1:12345/#token=" + strings.Repeat("a", 64)
	file, cleanup, err := browserLaunchFile(target)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if strings.Contains(file, "token=") || strings.Contains(file, strings.Repeat("a", 64)) {
		t.Fatal("bootstrap escaped into dispatcher argument")
	}
	u, err := url.Parse(file)
	if err != nil || u.Scheme != "file" {
		t.Fatal("not a file launcher", err)
	}
	path := u.Path
	if runtime.GOOS == "windows" {
		path = strings.TrimPrefix(path, "/")
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), target) {
		t.Fatal("launch document missing target", err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{path, filepath.Dir(path)} {
			st, e := os.Stat(p)
			if e != nil || st.Mode().Perm()&0077 != 0 {
				t.Fatal("launcher is not private", e)
			}
		}
	}
	cleanup()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cleanup left launcher", err)
	}
}
func TestBrowserRejectsNonLocalOrMissingBootstrap(t *testing.T) {
	for _, target := range []string{"https://example.org/", "http://127.0.0.1:12345/", "http://127.0.0.1:12345/#token=short", "http://127.0.0.1:12345/?x=y#token=" + strings.Repeat("a", 64)} {
		if OpenBrowser(target) == nil {
			t.Fatal("accepted unsafe launcher target")
		}
	}
}
