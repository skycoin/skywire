//go:build windows

package web

import (
	"os"
	"os/exec"
)

// setProcGroup is a no-op on Windows — there are no POSIX process groups, and
// the default CreateProcess behavior is sufficient for the web command's
// subprocess lifecycle.
func setProcGroup(_ *exec.Cmd) {}

// killProcGroup terminates the child process. Windows has no SIGINT-to-group
// equivalent, so this is a best-effort hard kill of the subprocess.
func killProcGroup(p *os.Process) {
	if p == nil {
		return
	}
	_ = p.Kill() //nolint:errcheck
}
