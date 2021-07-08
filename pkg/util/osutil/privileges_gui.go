//+build systray

package osutil

import (
	"github.com/gen2brain/dlgs"
	"os/exec"
	"syscall"
)

// GainRoot escalates privileges to gain root access. Returns `uid` to be stored.
func GainRoot() (int, error) {
	uid := syscall.Getuid()

	if err := syscall.Setuid(0); err != nil {
		pwd, success, err := dlgs.Password("Sudo", "your sudo password")
		if err != nil {
			return 0, err
		}

		if !success {
			return uid, err
		}
		exec.Command("echo", pwd, "|", "sudo", "-S")
	}

	return uid, nil
}

// ReleaseRoot releases root privileges, setting `oldUID`.
func ReleaseRoot(oldUID int) error {
	return syscall.Setuid(oldUID)
}
