//go:build darwin

// Package vpn pkg/vpn/os_client_darwin.go c3-app-vpn
package vpn

import (
	"bytes"
	"net/netip"
	"strings"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	defaultNetworkGatewayCMD = "netstat -rn | sed -n '/Internet/,/Internet6/p' | grep default | awk '{print $2}'"
)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (netip.Addr, error) {
	outputBytes, err := osutil.RunWithResult("sh", "-c", defaultNetworkGatewayCMD)
	if err != nil {
		return netip.Addr{}, err
	}

	outputBytes = bytes.TrimRight(outputBytes, "\n")

	lines := bytes.SplitSeq(outputBytes, []byte{'\n'})
	for l := range lines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			continue
		}

		if ip, err := netip.ParseAddr(string(l)); err == nil && ip.Unmap().Is4() {
			return ip.Unmap(), nil
		}
	}

	return netip.Addr{}, errCouldFindDefaultNetworkGateway
}

func setupClientSysPrivileges() (int, error) {
	value, err := osutil.GainRoot()
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		return value, errPermissionDenied
	}
	return value, err
}

func releaseClientSysPrivileges(oldUID int) error {
	return osutil.ReleaseRoot(oldUID)
}
