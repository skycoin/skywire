//+build linux

package netutil

import (
	"bytes"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	defaultNetworkInterfaceCMD = "ip r | awk '$1 == \"default\" {print $5}'"
)

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	outputBytes, err := osutil.RunWithResult("sh", "-c", defaultNetworkInterfaceCMD)
	if err != nil {
		return "", err
	}

	// just in case
	outputBytes = bytes.TrimRight(outputBytes, "\n")

	return string(outputBytes), nil
}
