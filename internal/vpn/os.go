package vpn

import (
	"fmt"
	"net"
	"os"
	"os/exec"
)

func parseCIDR(ipCIDR string) (ipStr, netmask string, err error) {
	ip, net, err := net.ParseCIDR(ipCIDR)
	if err != nil {
		return "", "", err
	}

	return ip.String(), fmt.Sprintf("%d.%d.%d.%d", net.Mask[0], net.Mask[1], net.Mask[2], net.Mask[3]), nil
}

//nolint:unparam
func run(bin string, args ...string) error {
	cmd := exec.Command(bin, args...) //nolint:gosec

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", bin, err)
	}

	return nil
}
