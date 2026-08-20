//go:build !unix

package client

import "os/exec"

// detach is a no-op where there is no process group to leave; the plugin ships
// for unix hosts, and this keeps the cross-compile vet honest.
func detach(cmd *exec.Cmd) {}
