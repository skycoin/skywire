//+build darwin

package vpn

import (
	"bytes"
	"net"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	defaultNetworkGatewayCMD = "netstat -rn | sed -n '/Internet/,/Internet6/p' | grep default | awk '{print $2}'"
)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	outputBytes, err := osutil.RunWithResult("sh", "-c", defaultNetworkGatewayCMD)
	if err != nil {
		return nil, err
	}

	outputBytes = bytes.TrimRight(outputBytes, "\n")

	lines := bytes.Split(outputBytes, []byte{'\n'})
	for _, l := range lines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, errCouldFindDefaultNetworkGateway
}

func setupClientSysPrivileges() (int, error) {
	return osutil.GainRoot()
}

func releaseClientSysPrivileges(oldUID int) error {
	return osutil.ReleaseRoot(oldUID)
}
