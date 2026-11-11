package main

import (
	"os/exec"
	"runtime"
	"testing"
)

// Every flag needs a line in deploy/re-psn.env.example and an old-name entry in its deprecated-names block;
// scripts/check-deploy.sh checks it, and the release build runs that script, so a flag added without the
// example would otherwise only fail on release day.
func TestDeployExampleAgreesWithFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the deploy check is a bash script; not run on Windows")
	}
	for _, tool := range []string{"bash", "go", "sed", "diff"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	cmd := exec.Command("bash", "scripts/check-deploy.sh")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scripts/check-deploy.sh: %v\n%s", err, out)
	}
}
