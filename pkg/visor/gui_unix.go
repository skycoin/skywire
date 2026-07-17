//go:build !windows && !withoutsystray
// +build !windows,!withoutsystray

// Package visor pkg/visor/gui_unix.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/util/osutil"
)

func platformExecUninstall() error {
	return osutil.Run("/bin/bash", "-c", deinstallerPath)
}
