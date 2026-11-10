package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHelpHidesToken: -h lists the flags with their defaults, so a status
// token read from the environment must not become the default it prints.
func TestHelpHidesToken(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the server in a subprocess")
	}
	const secret = "s3cret-help-token"
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(cleanEnv(), "RE_PSN_TEST_MAIN=1", "RE_PSN_TEST_ARGS=-h",
		"RE_PSN_HTTP_TOKEN="+secret+"-new", "PSNR_HTTP_TOKEN="+secret+"-old")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "-http-token") {
		t.Fatalf("-h did not list -http-token:\n%s", out)
	}
	if strings.Contains(string(out), "s3cret") {
		t.Fatalf("-h shows the token:\n%s", out)
	}
}

// TestStatusTokenSources: the token still reaches the status page from either
// variable name and from the command line, and the command line wins.
func TestStatusTokenSources(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"none", nil, nil, "token=false"},
		{"new variable", []string{"RE_PSN_HTTP_TOKEN=a"}, nil, "token=true"},
		{"old variable", []string{"PSNR_HTTP_TOKEN=a"}, nil, "token=true"},
		{"flag", nil, []string{"-http-token", "a"}, "token=true"},
		{"flag over empty variable", []string{"RE_PSN_HTTP_TOKEN="}, []string{"-http-token", "a"}, "token=true"},
		{"empty variable", []string{"RE_PSN_HTTP_TOKEN="}, nil, "token=false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := append([]string{"RE_PSN_HTTP=127.0.0.1:0"}, c.env...)
			_, log := startMain(t, env, c.args...)
			if !strings.Contains(log, "status page") || !strings.Contains(log, c.want) {
				t.Fatalf("want %q on the status page line:\n%s", c.want, log)
			}
		})
	}
}
