//go:build unix

package client

import (
	"os/exec"
	"syscall"
)

// detach puts the daemon in its own process group, so closing the pane that
// started it does not take it down with it (§2.4).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
