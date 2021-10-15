//go:build !windows
// +build !windows

package dmsgpty

import (
	"os"
	"path/filepath"
)

// Constants related to CLI.
const (
	DefaultCLINet = "unix"
)

// Constants related to dmsg.
const (
	DefaultPort     = uint16(22)
	DefaultCmd      = "/bin/bash"
	DefaultFlagExec = "-c"
)

// DefaultCLIAddr gets the default cli address (temp address)
func DefaultCLIAddr() string {
	return filepath.Join(os.TempDir(), "dmsgpty.sock")
}
