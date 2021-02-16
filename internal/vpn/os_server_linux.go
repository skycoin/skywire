//+build linux

package vpn

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"strings"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	defaultNetworkInterfaceCMD     = "ip r | awk '$1 == \"default\" {print $5}'"
	getIPv4ForwardingCMD           = "sysctl net.ipv4.ip_forward"
	getIPv6ForwardingCMD           = "sysctl net.ipv6.conf.all.forwarding"
	setIPv4ForwardingCMDFmt        = "sysctl -w net.ipv4.ip_forward=%s"
	setIPv6ForwardingCMDFmt        = "sysctl -w net.ipv6.conf.all.forwarding=%s"
	getIPTablesForwardPolicyCMD    = "iptables -L | grep \"Chain FORWARD\" | tr -d '()' | awk '{print $4}'"
	setIPTablesForwardPolicyCMDFmt = "iptables --policy FORWARD %s"
	enableIPMasqueradingCMDFmt     = "iptables -t nat -A POSTROUTING -o %s -j MASQUERADE"
	disableIPMasqueradingCMDFmt    = "iptables -t nat -D POSTROUTING -o %s -j MASQUERADE"
	blockIPToLocalNetCMDFmt        = "iptables -I FORWARD -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP && iptables -I INPUT -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP"
	allowIPToLocalNetCMDFmt        = "iptables -D FORWARD -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP && iptables -D INPUT -d 192.168.0.0/16,172.16.0.0/12,10.0.0.0/8 -s %s -j DROP"
)

// GetIPTablesForwardPolicy gets current policy for iptables `forward` chain.
func GetIPTablesForwardPolicy() (string, error) {
	output, err := osutil.RunWithResult("sh", "-c", getIPTablesForwardPolicyCMD)
	if err != nil {
		return "", fmt.Errorf("error running command %s: %w", getIPTablesForwardPolicyCMD, err)
	}

	outputBytes, err := ioutil.ReadAll(output)
	if err != nil {
		return "", fmt.Errorf("failed to read stdout: %w", err)
	}

	return strings.TrimRight(string(outputBytes), "\n"), nil
}

// SetIPTablesForwardPolicy sets `policy` for iptables `forward` chain.
func SetIPTablesForwardPolicy(policy string) error {
	cmd := fmt.Sprintf(setIPTablesForwardPolicyCMDFmt, policy)
	if err := osutil.Run("sh", "-c", cmd); err != nil { //nolint:gosec
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// SetIPTablesForwardAcceptPolicy sets ACCEPT policy for iptables `forward` chain.
func SetIPTablesForwardAcceptPolicy() error {
	const policy = "ACCEPT"
	return SetIPTablesForwardPolicy(policy)
}

// AllowIPToLocalNetwork allows all the packets coming from `source`
// to private IP ranges.
func AllowIPToLocalNetwork(src, dst net.IP) error {
	cmd := fmt.Sprintf(allowIPToLocalNetCMDFmt, src, src)
	if err := osutil.Run("sh", "-c", cmd); err != nil { //nolint:gosec
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// BlockIPToLocalNetwork blocks all the packets coming from `source`
// to private IP ranges.
func BlockIPToLocalNetwork(src, dst net.IP) error {
	cmd := fmt.Sprintf(blockIPToLocalNetCMDFmt, src, src)
	if err := osutil.Run("sh", "-c", cmd); err != nil { //nolint:gosec
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	output, err := osutil.RunWithResult("sh", "-c", defaultNetworkInterfaceCMD)
	if err != nil {
		return "", fmt.Errorf("error running command %s: %w", defaultNetworkInterfaceCMD, err)
	}

	outputBytes, err := ioutil.ReadAll(output)
	if err != nil {
		return "", fmt.Errorf("failed to read stdout: %w", err)
	}

	// just in case
	outputBytes = bytes.TrimRight(outputBytes, "\n")

	return string(outputBytes), nil
}

// GetIPv4ForwardingValue gets current value of IPv4 forwarding.
func GetIPv4ForwardingValue() (string, error) {
	return getIPForwardingValue(getIPv4ForwardingCMD)
}

// GetIPv6ForwardingValue gets current value of IPv6 forwarding.
func GetIPv6ForwardingValue() (string, error) {
	return getIPForwardingValue(getIPv6ForwardingCMD)
}

// SetIPv4ForwardingValue sets `val` value of IPv4 forwarding.
func SetIPv4ForwardingValue(val string) error {
	cmd := fmt.Sprintf(setIPv4ForwardingCMDFmt, val)
	if err := osutil.Run("sh", "-c", cmd); err != nil { //nolint:gosec
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// SetIPv6ForwardingValue sets `val` value of IPv6 forwarding.
func SetIPv6ForwardingValue(val string) error {
	cmd := fmt.Sprintf(setIPv6ForwardingCMDFmt, val)
	if err := osutil.Run("sh", "-c", cmd); err != nil { //nolint:gosec
		return fmt.Errorf("error running command %s: %w", cmd, err)
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
	if err := osutil.Run("sh", "-c", cmd); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(disableIPMasqueradingCMDFmt, ifcName)
	if err := osutil.Run("sh", "-c", cmd); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

func getIPForwardingValue(cmd string) (string, error) {
	out, err := osutil.RunWithResult("sh", "-c", cmd)
	if err != nil {
		return "", fmt.Errorf("error running command %s: %w", cmd, err)
	}

	outBytes, err := ioutil.ReadAll(out)
	if err != nil {
		return "", fmt.Errorf("failed to read stdout: %w", err)
	}

	val, err := parseIPForwardingOutput(outBytes)
	if err != nil {
		return "", fmt.Errorf("error parsing output of command %s: %w", cmd, err)
	}

	return val, nil
}

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{'='})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
