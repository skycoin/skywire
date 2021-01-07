//+build darwin

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strconv"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	osxServiceIdentifier = "com.skycoin.skywire.visor"
)

func isVisorRunning() (bool, error) {
	cmd := "launchctl list | grep " + osxServiceIdentifier + " | awk {'print $1'}"

	stdout, err := osutil.RunWithResult("/bin/bash", "-c", cmd)
	if err != nil {
		return false, fmt.Errorf("failed to run command %s: %w", cmd, err)
	}

	output, err := ioutil.ReadAll(stdout)
	if err != nil {
		return false, fmt.Errorf("failed to read command output: %w", err)
	}

	output = bytes.TrimSpace(output)

	if _, err := strconv.Atoi(string(output)); err != nil {
		// in this case there's either `-` returned instead of pid, or
		// something else, but the process is not running anyway
		return false, nil
	}

	return true, nil
}

func startVisorDaemon() error {
	cmd := "launchctl start " + osxServiceIdentifier

	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		return fmt.Errorf("failed to run command %s: %w", cmd, err)
	}

	return nil
}

func stopVisorDaemon() error {
	cmd := "launchctl stop " + osxServiceIdentifier

	if err := osutil.Run("/bin/bash", "-c", cmd); err != nil {
		return fmt.Errorf("failed to run command %s: %w", cmd, err)
	}

	return nil
}
