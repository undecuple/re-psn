//go:build !windows

package host

import (
	"os/exec"
	"syscall"
)

func ownChild(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
func stopChild(cmd *exec.Cmd) error          { return cmd.Process.Signal(syscall.SIGTERM) }
