package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	osxServiceIdentifier = "com.skycoin.skywire.visor"
)

var (
	log = logging.MustGetLogger("skywire_deinstaller")
)

func main() {
	suid := syscall.Getuid()

	if err := syscall.Setuid(0); err != nil {
		log.WithError(err).Errorln("failed to setuid 0")
		os.Exit(1)
	}

	if err := uninstall(); err != nil {
		log.WithError(err).Errorln("failed to uninstall skywire apps")
		return
	}

	/*cmd := "installer -pkg /Users/darkrengarius/go/src/github.com/SkycoinPro/skywire-services/scripts/mac_installer/remover.pkg -target /"
	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		mUninstall.Enable()
		log.WithError(err).Errorln("failed to remove systray app")
		if err := syscall.Setuid(suid); err != nil {
			log.WithError(err).Errorln("Failed to revert uid")
		}
		return
	}*/

	if err := syscall.Setuid(suid); err != nil {
		log.WithError(err).Errorln("Failed to revert uid")
	}
}

func uninstall() error {
	const logCleanerServiceIdentifier = "com.skycoin.skywire.logcleaner"

	const uninstallScript = `
launchctl remove ` + logCleanerServiceIdentifier + `
launchctl remove ` + osxServiceIdentifier + `
sleep 2

rm -rf $HOME/Library/LaunchAgents/` + logCleanerServiceIdentifier + `.plist
rm -rf $HOME/Library/LaunchAgents/` + osxServiceIdentifier + `.plist

#sudo sed -i '' '/.*skywire.*/d' /etc/newsyslog.conf

pkgutil --forget ` + osxServiceIdentifier + `
pkgutil --forget com.skycoin.skywire.updater
pkgutil --forget com.skycoin.skywire.remover

sudo rm -rf /opt/skywire

exit 0
`

	if err := osutil.Run("/bin/bash", "-c", uninstallScript); err != nil {
		return fmt.Errorf("failed to run uninstall script: %w", err)
	}

	return nil
}
