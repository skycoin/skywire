//go:build !windows && !withoutsystray
// +build !windows,!withoutsystray

package gui

import (
	"github.com/skycoin/skywire/pkg/util/osutil"
)

func platformExecUninstall() error {
	return osutil.Run("/bin/bash", "-c", deinstallerPath)
}
