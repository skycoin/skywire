//+build darwin

package netutil

import (
	"bytes"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	activeNetworkInterfaceCMD = "netstat -rn | sed -n '/Internet/,/Internet6/p' | grep default | awk '{print $4}'"
)

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	stdout, err := osutil.RunWithResult("sh", "-c", activeNetworkInterfaceCMD)
	if err != nil {
		return "", err
	}

	stdout = bytes.TrimSpace(stdout)

	return string(stdout), nil
}
