//go:build windows

// Package vpn pkg/vpn/os_client_windows.go c3-app-vpn
package vpn

import (
	"bytes"
	"net/netip"
	"regexp"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	defaultNetworkGatewayCMD = "route PRINT"
)

var redundantWhitespacesCleanupRegex = regexp.MustCompile(`[\s\p{Zs}]{2,}`)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (netip.Addr, error) {
	outBytes, err := osutil.RunWithResult("cmd", "/C", defaultNetworkGatewayCMD)
	if err != nil {
		return netip.Addr{}, err
	}

	outBytes = bytes.TrimRight(outBytes, "\n\r")

	lines := bytes.SplitSeq(outBytes, []byte{'\n'})
	for line := range lines {
		line = bytes.TrimLeft(line, " \t\r\n")
		if !bytes.HasPrefix(line, []byte("0.0.0.0")) {
			continue
		}

		line = bytes.TrimRight(line, " \t\r\n")

		line := redundantWhitespacesCleanupRegex.ReplaceAll(line, []byte{' '})

		lineTokens := bytes.Split(line, []byte{' '})
		if len(lineTokens) < 3 {
			continue
		}

		if ip, err := netip.ParseAddr(string(lineTokens[2])); err == nil && ip.Unmap().Is4() {
			return ip.Unmap(), nil
		}
	}

	return netip.Addr{}, errCouldFindDefaultNetworkGateway
}

func setupClientSysPrivileges() (suid int, err error) { //nolint:unused
	return 0, nil
}
