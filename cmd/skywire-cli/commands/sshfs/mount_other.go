//go:build !linux

// Package clisshfs cmd/skywire-cli/commands/sshfs/mount_other.go —
// non-Linux stub. The sshfs subsystem itself is cross-platform
// (server-side compiles everywhere), but the FUSE client only ships
// on Linux today. macOS/Windows would need separate platform code
// (macFUSE / WinFsp); not in v1 scope.
package clisshfs

import (
	"errors"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(mountStubCmd, umountStubCmd)
}

var errSshfsNotSupported = errors.New("sshfs: FUSE mount is Linux-only in this build (macOS/Windows support would require macFUSE/WinFsp wiring not present here)")

var mountStubCmd = &cobra.Command{
	Use:           "mount <pk>@<host>:<port> <mountpoint>",
	Short:         "Mount a peer visor's filesystem (Linux only)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return errSshfsNotSupported
	},
}

var umountStubCmd = &cobra.Command{
	Use:           "umount <mountpoint>",
	Short:         "Unmount a previously-mounted sshfs (Linux only)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return errSshfsNotSupported
	},
}
