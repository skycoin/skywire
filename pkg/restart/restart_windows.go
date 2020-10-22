// +build windows

package restart

import "os/exec"

func attachTTY(*exec.Cmd) {
	// not used for Windows

	return
}
