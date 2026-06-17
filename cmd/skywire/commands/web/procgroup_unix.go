//go:build !windows

package web

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcGroup puts the child in its own process group so the whole group can
// be signaled on cancel / client-disconnect (matters for visor halt, dmsg curl
// downloads, etc).
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcGroup sends SIGINT to the child's process group for graceful cleanup.
func killProcGroup(p *os.Process) {
	if p == nil {
		return
	}
	_ = syscall.Kill(-p.Pid, syscall.SIGINT) //nolint:errcheck
}
