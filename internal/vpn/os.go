// Package vpn internal/vpn/os.go
package vpn

import (
	"fmt"
	"net"
)

func parseCIDR(ipCIDR string) (ipStr, netmask string, err error) { //nolint : actually used in os_windows.go
	ip, net, err := net.ParseCIDR(ipCIDR)
	if err != nil {
		return "", "", err
	}

	return ip.String(), fmt.Sprintf("%d.%d.%d.%d", net.Mask[0], net.Mask[1], net.Mask[2], net.Mask[3]), nil
}
