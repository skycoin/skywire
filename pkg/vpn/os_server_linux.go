//go:build linux
// +build linux

// Package vpn pkg/vpn/os_server_linux.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

const (
	getIPTablesForwardPolicyCMD    = "iptables -L | grep \"Chain FORWARD\" | tr -d '()' | awk '{print $4}'"
	setIPTablesForwardPolicyCMDFmt = "iptables --policy FORWARD %s"
	enableIPMasqueradingCMDFmt     = "iptables -t nat -A POSTROUTING -o %s -j MASQUERADE"
	disableIPMasqueradingCMDFmt    = "iptables -t nat -D POSTROUTING -o %s -j MASQUERADE"
	blockIPToLocalNetCMDFmt        = "iptables -I FORWARD -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP && iptables -I INPUT -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP"
	allowIPToLocalNetCMDFmt        = "iptables -D FORWARD -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP && iptables -D INPUT -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP"
)

// GetIPTablesForwardPolicy gets current policy for iptables `forward` chain.
func GetIPTablesForwardPolicy() (string, error) {
	outputBytes, err := osutil.RunElevatedWithResult("sh", "-c", getIPTablesForwardPolicyCMD)
	if err != nil {
		return "", err
	}
	if len(outputBytes) == 0 {
		return "", errPermissionDenied
	}
	return strings.TrimRight(string(outputBytes), "\n"), nil
}

// SetIPTablesForwardPolicy sets `policy` for iptables `forward` chain.
func SetIPTablesForwardPolicy(policy string) error {
	cmd := fmt.Sprintf(setIPTablesForwardPolicyCMDFmt, policy)
	return osutil.RunElevated("sh", "-c", cmd)
}

// SetIPTablesForwardAcceptPolicy sets ACCEPT policy for iptables `forward` chain.
func SetIPTablesForwardAcceptPolicy() error {
	const policy = "ACCEPT"
	return SetIPTablesForwardPolicy(policy)
}

// AllowIPToLocalNetwork allows all the packets coming from `source`
// to private IP ranges.
func AllowIPToLocalNetwork(src, _ net.IP) error {
	cmd := fmt.Sprintf(allowIPToLocalNetCMDFmt, src, src)
	return osutil.RunElevated("sh", "-c", cmd)
}

// BlockIPToLocalNetwork blocks all the packets coming from `source`
// to private IP ranges.
func BlockIPToLocalNetwork(src, _ net.IP) error {
	cmd := fmt.Sprintf(blockIPToLocalNetCMDFmt, src, src)
	return osutil.RunElevated("sh", "-c", cmd)
}

// GetIPv4ForwardingValue gets current value of IPv4 forwarding (via /proc — no
// sysctl binary needed).
func GetIPv4ForwardingValue() (string, error) {
	return netctl.GetIPv4Forwarding()
}

// GetIPv6ForwardingValue gets current value of IPv6 forwarding (via /proc).
func GetIPv6ForwardingValue() (string, error) {
	return netctl.GetIPv6Forwarding()
}

// SetIPv4ForwardingValue sets `val` value of IPv4 forwarding (via /proc).
func SetIPv4ForwardingValue(val string) error {
	if err := netctl.SetIPv4Forwarding(val); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return errPermissionDenied
		}
		return err
	}
	return nil
}

// SetIPv6ForwardingValue sets `val` value of IPv6 forwarding (via /proc).
func SetIPv6ForwardingValue(val string) error {
	if err := netctl.SetIPv6Forwarding(val); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return errPermissionDenied
		}
		return err
	}
	return nil
}

// EnableIPv4Forwarding enables IPv4 forwarding.
func EnableIPv4Forwarding() error {
	return SetIPv4ForwardingValue("1")
}

// EnableIPv6Forwarding enables IPv6 forwarding.
func EnableIPv6Forwarding() error {
	return SetIPv6ForwardingValue("1")
}

// EnableIPMasquerading enables IP masquerading for the interface with name `ifcName`.
func EnableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(enableIPMasqueradingCMDFmt, ifcName)
	return osutil.RunElevated("sh", "-c", cmd)
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(disableIPMasqueradingCMDFmt, ifcName)
	return osutil.RunElevated("sh", "-c", cmd)
}
