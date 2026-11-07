package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// These fixtures exercise the real HTTP handler and logger. The optional file
// feeds tools/check_fail2ban.py; the assertions always run without that tool.
func TestHTTPStatusFail2banFixtures(t *testing.T) {
	type fixture struct {
		Name string `json:"name"`
		Line string `json:"line"`
		IP   string `json:"ip"`
	}
	cases := []struct {
		name, remote, path, format, token, header, query, kind, addr, match string
		forwarded                                                           bool
		status                                                              int
	}{
		{name: "missing-v4", remote: "198.51.100.12:44100", path: "/api/stats", addr: "198.51.100.12", match: "198.51.100.12", status: 401},
		{name: "wrong-v6", remote: "[2001:db8::12]:44100", path: "/", header: "Bearer fixture-wrong", addr: "2001:db8::12", match: "2001:db8::12", status: 401},
		{name: "valid-header", remote: "198.51.100.12:44100", path: "/api/stats", header: "Bearer fixture-secret", query: "fixture-wrong", status: 200},
		{name: "valid-query", remote: "198.51.100.12:44100", path: "/", query: "fixture-secret", status: 200},
		{name: "token-off", remote: "198.51.100.12:44100", path: "/api/stats", token: "off", status: 200},
		{name: "quoted-path", remote: "198.51.100.13:44100", path: `/a","addr":"203.0.113.99","path":"/`, addr: "198.51.100.13", match: "198.51.100.13", status: 401},
		{name: "newline-path", remote: "198.51.100.14:44100", path: "/a\n" + `{"time":"x","level":"WARN","msg":"status page refused","addr":"203.0.113.99","path":"/","reason":"token missing or wrong"}`, addr: "198.51.100.14", match: "198.51.100.14", status: 401},
		{name: "untrusted-forwarded", remote: "198.51.100.15:44100", path: "/api/stats", forwarded: true, addr: "198.51.100.15", match: "198.51.100.15", status: 401},
		{name: "hostname-refused", remote: "untrusted.example:44100", path: "/api/stats", addr: "untrusted.example", status: 401},
		{name: "default-text", remote: "198.51.100.16:44100", path: "/api/stats", format: "text", addr: "198.51.100.16", status: 401},
		{name: "info-level", kind: "info", addr: "198.51.100.17"},
		{name: "other-message", kind: "other", addr: "198.51.100.18"},
	}
	var fixtures []fixture
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "quoted-path" && !strings.Contains(tc.path, `","addr":"203.0.113.99"`) {
				t.Fatal("hostile fixture must contain literal JSON quote delimiters")
			}
			if tc.name == "newline-path" && (!strings.Contains(tc.path, "\n") || !strings.Contains(tc.path, `{"time":"x"`)) {
				t.Fatal("hostile fixture must contain a real newline and literal forged JSON")
			}
			s := NewServer(10, false)
			var buf bytes.Buffer
			format := tc.format
			if format == "" {
				format = "json"
			}
			s.log = newLogger(&buf, levelFor(false), format)
			s.httpToken = "fixture-secret"
			if tc.token == "off" {
				s.httpToken = ""
			}
			switch tc.kind {
			case "info":
				s.log.Info("status page refused", "addr", tc.addr, "path", "/api/stats", "reason", "token missing or wrong")
			case "other":
				s.log.Warn("request refused", "addr", tc.addr, "path", "/api/stats", "reason", "token missing or wrong")
			default:
				r := httptest.NewRequest(http.MethodGet, "http://status.invalid/", nil)
				r.RemoteAddr = tc.remote
				r.URL.Path = tc.path
				if tc.query != "" {
					r.URL.RawQuery = "token=" + tc.query
				}
				if tc.header != "" {
					r.Header.Set("Authorization", tc.header)
				}
				if tc.forwarded {
					r.Header.Set("X-Forwarded-For", "203.0.113.99")
					r.Header.Set("CF-Connecting-IP", "203.0.113.99")
					r.Header.Set("Forwarded", "for=203.0.113.99")
				}
				w := httptest.NewRecorder()
				s.HTTP().ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatalf("HTTP status %d, want %d", w.Code, tc.status)
				}
			}
			line := buf.String()
			if strings.Contains(line, "fixture-secret") || strings.Contains(line, "fixture-wrong") {
				t.Fatal("token appeared in log")
			}
			if tc.kind == "" && tc.status == http.StatusOK {
				if line != "" {
					t.Fatalf("successful HTTP request logged a refusal: %q", line)
				}
			} else {
				if strings.Count(line, "\n") != 1 {
					t.Fatalf("logger must emit exactly one line: %q", line)
				}
				if format == "json" {
					var record map[string]string
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatal(err)
					}
					if record["addr"] != tc.addr || record["time"] == "" || record["reason"] != "token missing or wrong" {
						t.Fatalf("unexpected log record: %v", record)
					}
					if tc.kind == "" && (record["path"] != tc.path || record["level"] != "WARN" || record["msg"] != "status page refused") {
						t.Fatalf("HTTP refusal fields changed: %v", record)
					}
					if tc.forwarded && strings.Contains(line, "203.0.113.99") {
						t.Fatal("untrusted forwarded address appeared in log")
					}
				}
			}
			fixtures = append(fixtures, fixture{Name: tc.name, Line: strings.TrimSuffix(line, "\n"), IP: tc.match})
		})
	}
	if t.Failed() {
		return
	}
	if path := os.Getenv("RE_PSN_FAIL2BAN_FIXTURES"); path != "" {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewEncoder(f).Encode(fixtures)
		closeErr := f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}
