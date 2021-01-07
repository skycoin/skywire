//+build darwin

package main

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	linuxServiceName = "skywire"
)

func isVisorRunning() (bool, error) {
	cmd := "systemctl is-active --quiet " + linuxServiceName

	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		// if cmd doesn't return 0 status code, daemon is considered not running
		return false, nil
	}

	return true, nil
}

func startVisorDaemon() error {
	cmd := "systemctl start " + linuxServiceName

	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		return fmt.Errorf("failed to run command %s: %w", cmd, err)
	}

	return nil
}

func stopVisorDaemon() error {
	cmd := "systemctl stop " + linuxServiceName

	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		return fmt.Errorf("failed to run command %s: %w", cmd, err)
	}

	return nil
}
