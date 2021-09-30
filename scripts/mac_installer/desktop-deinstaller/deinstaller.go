package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/util/osutil"
)

var (
	log = logging.MustGetLogger("skywire_deinstaller")
)

func main() {
	if err := uninstall(); err != nil {
		log.WithError(err).Errorln("failed to uninstall skywire")
		os.Exit(1)
	}
}

func uninstall() error {
	const (
		osxServiceIdentifier        = "com.skycoin.skywire.visor"
		logCleanerServiceIdentifier = "com.skycoin.skywire.logcleaner"
	)

	const logCleanerUninstallScript = `
launchctl remove ` + logCleanerServiceIdentifier + `

rm -rf $HOME/Library/LaunchAgents/` + logCleanerServiceIdentifier + `.plist

exit 0
`

	if err := osutil.Run("/bin/bash", "-c", logCleanerUninstallScript); err != nil {
		return fmt.Errorf("failed to run uninstall script: %w", err)
	}

	uninstallScript := `
if pgrep vpn-client; then skywire-cli visor stop-app vpn-client; fi
if pgrep skywire; then pkill -f skywire; fi
pkgutil --forget ` + osxServiceIdentifier + `
pkgutil --forget com.skycoin.skywire.updater
pkgutil --forget com.skycoin.skywire.remover

rm -rf ` + filepath.Join(skyenv.PackageSkywirePath(), "local") + `
rm -rf ` + filepath.Join(os.Getenv("HOME"), "Library", "Logs", "skywire") + `
unlink /usr/local/bin/skywire-cli
rm -rf /Applications/Skywire.app
`

	uid := syscall.Getuid()

	if err := syscall.Setuid(0); err != nil {
		return fmt.Errorf("failed to setuid 0: %w", err)
	}

	if err := osutil.Run("/bin/bash", "-c", uninstallScript); err != nil {
		return fmt.Errorf("failed to remove installation directory: %w", err)
	}

	if err := syscall.Setuid(uid); err != nil {
		log.WithError(err).Errorln("Failed to revert uid")
	}

	return nil
}
