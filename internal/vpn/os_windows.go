//+build windows

package vpn

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
)

const (
	defaultNetworkGatewayCMD = "route PRINT"
	tunSetupCMDFmt           = "netsh interface ip set address \"%s\" static %s %s %s"
	tunMTUSetupCMDFmt        = "netsh interface ipv4 set subinterface \"%s\" mtu=%d"
	addRouteCMDFmt           = "route add %s mask %s %s"
	deleteRouteCMDFmt        = "route delete %s mask %s %s"
)

var redundantWhitespacesCleanupRegex = regexp.MustCompile(`[\s\p{Zs}]{2,}`)

func DefaultNetworkGateway() (net.IP, error) {
	cmd := exec.Command("cmd", "/C", defaultNetworkGatewayCMD)
	outBytes, err := cmd.Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("error running command %s: %w", defaultNetworkGatewayCMD, err)
	}

	outBytes = bytes.TrimRight(outBytes, "\n\r")

	outLines := bytes.Split(outBytes, []byte{'\n'})

	for _, line := range outLines {
		line = bytes.TrimLeft(line, " \t\r\n")
		if !bytes.HasPrefix(line, []byte("0.0.0.0")) {
			continue
		}

		line = bytes.TrimRight(line, " \t\r\n")

		line := redundantWhitespacesCleanupRegex.ReplaceAll(line, []byte{' '})

		lineTokens := bytes.Split(line, []byte{' '})
		if len(lineTokens) < 2 {
			continue
		}

		ip := net.ParseIP(string(lineTokens[2]))
		if ip != nil && ip.To4() != nil {
			return ip, nil
		}
	}

	return nil, errors.New("couldn't find default network gateway")
}

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	setupCmd := fmt.Sprintf(tunSetupCMDFmt, ifcName, ip, netmask, gateway)
	if err := run("cmd", "/C", setupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", setupCmd, err)
	}

	mtuSetupCmd := fmt.Sprintf(tunMTUSetupCMDFmt, ifcName, mtu)
	if err := run("cmd", "/C", mtuSetupCmd); err != nil {
		return fmt.Errorf("error running command %s: %w", mtuSetupCmd, err)
	}

	return nil
}

// AddRoute adds route to `ipCIDR` through the `gateway` to the OS routing table.
func AddRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	cmd := fmt.Sprintf(addRouteCMDFmt, ip, netmask, gateway)
	return run("cmd", "/C", cmd)
}

// DeleteRoute removes route to `ipCIDR` through the `gateway` from the OS routing table.
func DeleteRoute(ipCIDR, gateway string) error {
	ip, netmask, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	cmd := fmt.Sprintf(deleteRouteCMDFmt, ip, netmask, gateway)
	return run("cmd", "/C", cmd)
}
