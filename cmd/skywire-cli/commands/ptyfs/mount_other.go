//go:build !linux

// Package cliptyfs cmd/skywire-cli/commands/ptyfs/mount_other.go c4-vis-cli
// non-Linux stub. The ptyfs subsystem itself is cross-platform
// (server-side compiles everywhere), but the FUSE client only ships
// on Linux today. macOS/Windows would need separate platform code
// (macFUSE / WinFsp); not in v1 scope.
package cliptyfs

import (
	"errors"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(mountStubCmd, umountStubCmd)
}

var errPtyfsNotSupported = errors.New("ptyfs: FUSE mount is Linux-only in this build (macOS/Windows support would require macFUSE/WinFsp wiring not present here)")

var mountStubCmd = &cobra.Command{
	Use:           "mount <pk>@<host>:<port> <mountpoint>",
	Short:         "Mount a peer visor's filesystem (Linux only)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return errPtyfsNotSupported
	},
}

var umountStubCmd = &cobra.Command{
	Use:           "umount <mountpoint>",
	Short:         "Unmount a previously-mounted pty fs (Linux only)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return errPtyfsNotSupported
	},
}
