//+build darwin

package vpn

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
)

const (
	defaultNetworkGatewayCMD = "netstat -rn | sed -n '/Internet/,/Internet6/p' | grep default | awk '{print $2}'"
)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	outputBytes, err := exec.Command("sh", "-c", defaultNetworkGatewayCMD).Output()
	if err != nil {
		return nil, fmt.Errorf("error running command %s: %w", defaultNetworkGatewayCMD, err)
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
