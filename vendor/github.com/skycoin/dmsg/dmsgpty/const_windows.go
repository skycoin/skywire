//+build windows

package dmsgpty

import (
	"os"
	"path/filepath"
)

const (
	DefaultCLINet = "unix"
)

// Constants related to dmsg.
const (
	DefaultPort = uint16(22)
	DefaultCmd  = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
)

// DefaultCLIAddr gets the default cli address
func DefaultCLIAddr() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		homedir = os.TempDir()
	}
	return filepath.Join(homedir, "dmsgpty.sock")
}
