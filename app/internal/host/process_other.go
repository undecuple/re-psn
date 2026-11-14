//go:build !windows && !linux

package host

import "os/exec"

func configureChild(cmd *exec.Cmd) {}
