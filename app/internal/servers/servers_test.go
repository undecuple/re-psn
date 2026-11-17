package servers

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeGivesOneForm(t *testing.T) {
	cases := []struct{ in, want Entry }{
		{Entry{" Alice ", " Play.Example.ORG:36100 ", " HTTPS://Play.Example.ORG:443/status "}, Entry{"Alice", "play.example.org:36100", "https://play.example.org/status"}},
		{Entry{"a", "Host.:1234", "http://Host.:80/"}, Entry{"a", "host:1234", "http://host/"}},
		{Entry{"a", "[::1]:36100", "http://[::1]:8080/s"}, Entry{"a", "[::1]:36100", "http://[::1]:8080/s"}},
		{Entry{"a", "h:1", "https://h:8443/s"}, Entry{"a", "h:1", "https://h:8443/s"}},
		{Entry{"a", "not an address", "nonsense"}, Entry{"a", "not an address", "nonsense"}},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestAddRejectsBadEntries(t *testing.T) {
	long := strings.Repeat("x", MaxNameRunes+1)
	bad := []Entry{
		{"", "h:1", ""},
		{"   ", "h:1", ""},
		{long, "h:1", ""},
		{"ctl\x07", "h:1", ""},
		{"bad\xff", "h:1", ""},
		{"a", "no-port", ""},
		{"a", "h:0", ""},
		{"a", "h:70000", ""},
		{"a", "h:1", "ftp://h/s"},
		{"a", "h:1", "https://user:pass@h/s"},
		{"a", "h:1", "https://user@h/s"},
		{"a", "h:1", "//h/s"},
	}
	for _, e := range bad {
		var l List
		if err := l.Add(e); err == nil {
			t.Errorf("Add(%+v) accepted a bad entry", e)
		}
		if len(l.Entries) != 0 {
			t.Errorf("Add(%+v) left %d entries behind", e, len(l.Entries))
		}
	}
	var l List
	if err := l.Add(Entry{strings.Repeat("é", MaxNameRunes), "h:1", ""}); err != nil {
		t.Errorf("a name of exactly %d characters was refused: %v", MaxNameRunes, err)
	}
}

func TestDuplicatesAreFoundAfterNormalizing(t *testing.T) {
	var l List
	if err := l.Add(Entry{"one", "Host:1234", ""}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{{"two", "host:1234", ""}, {"three", "HOST.:1234", ""}, {"four", " host:1234", ""}} {
		if err := l.Add(e); err == nil {
			t.Errorf("Add(%+v) accepted a duplicate", e)
		}
	}
	// The same server with a different status URL is a different entry; a default port in the URL is not a difference.
	if err := l.Add(Entry{"five", "host:1234", "https://host/s"}); err != nil {
		t.Errorf("a different status URL was refused: %v", err)
	}
	if err := l.Add(Entry{"six", "host:1234", "https://HOST:443/s"}); err == nil {
		t.Error("a status URL that differs only by case and default port was accepted")
	}
	if len(l.Entries) != 2 {
		t.Errorf("want 2 entries, have %d", len(l.Entries))
	}
}

func TestListIsCapped(t *testing.T) {
	var l List
	for i := 0; i < MaxEntries; i++ {
		if err := l.Add(Entry{"s", "h:" + itoa(i+1), ""}); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
	if err := l.Add(Entry{"s", "h:9999", ""}); err == nil {
		t.Error("the list grew past its cap")
	}
	// Merge holds the cap too, and counts what it skips.
	var m List
	for i := 0; i < MaxEntries-2; i++ {
		_ = m.Add(Entry{"s", "m:" + itoa(i+1), ""})
	}
	other := []Entry{{"a", "n:1", ""}, {"b", "n:2", ""}, {"c", "n:3", ""}, {"dup", "m:1", ""}, {"bad", "nope", ""}}
	added, skipped := m.Merge(other)
	if added != 2 || skipped != 3 || len(m.Entries) != MaxEntries {
		t.Errorf("Merge added %d skipped %d, list has %d (want 2, 3, %d)", added, skipped, len(m.Entries), MaxEntries)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestRemove(t *testing.T) {
	var l List
	_ = l.Add(Entry{"a", "h:1", ""})
	_ = l.Add(Entry{"b", "h:2", ""})
	_ = l.Add(Entry{"c", "h:3", ""})
	if !l.Remove(Key(Entry{"b", "h:2", ""})) {
		t.Fatal("Remove did not find an entry that is there")
	}
	if l.Remove(Key(Entry{"b", "h:2", ""})) {
		t.Error("Remove found an entry twice")
	}
	if len(l.Entries) != 2 || l.Entries[0].Name != "a" || l.Entries[1].Name != "c" {
		t.Errorf("wrong entries left: %+v", l.Entries)
	}
}

func TestLocalNetwork(t *testing.T) {
	local := []Entry{
		{"a", "127.0.0.1:36100", ""}, {"a", "[::1]:36100", ""}, {"a", "localhost:36100", ""}, {"a", "10.1.2.3:1", ""},
		{"a", "192.168.0.5:1", ""}, {"a", "172.16.0.1:1", ""}, {"a", "169.254.169.254:1", ""}, {"a", "0.0.0.0:1", ""},
		{"a", "x.example.org:1", "http://127.0.0.1:36101/"}, {"a", "x.example.org:1", "https://localhost/s"},
		{"a", "x.example.org:1", "http://[fe80::1]/s"}, {"a", "app.localhost:1", ""},
	}
	for _, e := range local {
		if !LocalNetwork(e) {
			t.Errorf("%+v was not marked as a local network address", e)
		}
	}
	// A name that merely resolves to a private address is not caught: only literals and localhost are recognised.
	public := []Entry{{"a", "play.example.org:36100", "https://play.example.org/status"}, {"a", "8.8.8.8:1", ""}, {"a", "internal.corp:1", ""}}
	for _, e := range public {
		if LocalNetwork(e) {
			t.Errorf("%+v was marked as a local network address", e)
		}
	}
}

const good = `{"version": 1, "servers": [{"name": "Alice", "server": "play.example.org:36100", "status_url": "https://play.example.org/status"}, {"name": "Bob", "server": "10.0.0.2:36100"}]}`

func TestParseAcceptsItsOwnFormat(t *testing.T) {
	l, err := Parse(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 || l.Entries[0].StatusURL != "https://play.example.org/status" || l.Entries[1].StatusURL != "" {
		t.Errorf("wrong entries: %+v", l.Entries)
	}
	// Entries in a hand-edited file are put in their normal form.
	l, err = Parse(strings.NewReader(`{"version":1,"servers":[{"name":" X ","server":"Host.:1","status_url":"HTTP://Host:80/s"}]}`))
	if err != nil || l.Entries[0] != (Entry{"X", "host:1", "http://host/s"}) {
		t.Errorf("not normalized: %+v, %v", l, err)
	}
}

func TestParseIsStrict(t *testing.T) {
	big := `{"version":1,"servers":[` + strings.Repeat(`{"name":"a","server":"h:1"},`, MaxEntries) + `{"name":"a","server":"h:2"}]}`
	bad := map[string]string{
		"empty":            ``,
		"not json":         `servers`,
		"wrong version":    `{"version": 2, "servers": []}`,
		"no version":       `{"servers": []}`,
		"unknown field":    `{"version": 1, "servers": [], "extra": true}`,
		"unknown in entry": `{"version": 1, "servers": [{"name":"a","server":"h:1","token_file":"/x"}]}`,
		"trailing data":    `{"version": 1, "servers": []} {"version": 1}`,
		"trailing junk":    `{"version": 1, "servers": []} x`,
		"bad address":      `{"version": 1, "servers": [{"name":"a","server":"nope"}]}`,
		"credentials":      `{"version": 1, "servers": [{"name":"a","server":"h:1","status_url":"https://u:p@h/s"}]}`,
		"duplicate":        `{"version": 1, "servers": [{"name":"a","server":"h:1"},{"name":"b","server":"H:1"}]}`,
		"too many":         big,
		"too big":          `{"version":1,"servers":[],"pad":"` + strings.Repeat("x", MaxFileBytes) + `"}`,
	}
	for name, in := range bad {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	var l List
	_ = l.Add(Entry{"Alice", "play.example.org:36100", "https://play.example.org/status"})
	_ = l.Add(Entry{"Bob <img src=x onerror=alert(1)>", "10.0.0.2:36100", ""})
	b, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Error("no final newline")
	}
	back, err := Parse(strings.NewReader(string(b)))
	if err != nil || len(back.Entries) != 2 || back.Entries[1].Name != l.Entries[1].Name {
		t.Errorf("round trip: %+v, %v", back, err)
	}
	var empty List
	b, _ = empty.Marshal()
	if !strings.Contains(string(b), `"servers": []`) {
		t.Errorf("an empty list must write an empty array, got %s", b)
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", "re-psn", FileName)
	if l, err := Load(path); err != nil || len(l.Entries) != 0 {
		t.Fatalf("a missing file must be an empty list: %+v, %v", l, err)
	}
	var l List
	_ = l.Add(Entry{"Alice", "play.example.org:36100", ""})
	if err := Save(path, &l); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || len(back.Entries) != 1 || back.Entries[0].Name != "Alice" {
		t.Fatalf("load after save: %+v, %v", back, err)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v, err %v; want 0600", st.Mode().Perm(), err)
		}
		if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("folder mode %v, err %v; want 0700", st.Mode().Perm(), err)
		}
	}
	// A second save replaces the first and leaves no temporary file behind.
	_ = l.Add(Entry{"Bob", "10.0.0.2:36100", ""})
	if err := Save(path, &l); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the folder holds %d files, want only the list", len(entries))
	}
}

func TestCorruptFileIsRefusedAndKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	const junk = `{"version": 1, "servers": [ not json`
	if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load of a corrupt file: %v, want an error that is not 'missing'", err)
	}
	if b, _ := os.ReadFile(path); string(b) != junk {
		t.Error("loading a corrupt file changed it")
	}
}

func TestPathForIsBesideTheSettingsFile(t *testing.T) {
	got := PathFor(filepath.Join("home", "u", ".config", "re-psn", "re-psn.ini"))
	if want := filepath.Join("home", "u", ".config", "re-psn", FileName); got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
}
