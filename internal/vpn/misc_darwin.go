//+build darwin

package vpn

import (
	"bytes"
	"fmt"
)

const (
	gatewayForIfcCMDFmt     = "/usr/sbin/netstat -rn | /usr/bin/grep default | /usr/bin/grep %s | /usr/bin/awk '{print $2}'"
	setIPv4ForwardingCMDFmt = "sysctl -w net.inet.ip.forwarding=%s"
	setIPv6ForwardingCMDFmt = "sysctl -w net.inet6.ip6.forwarding=%s"
	getIPv4ForwardingCMD    = "sysctl net.inet.ip.forwarding"
	getIPv6ForwardingCMD    = "sysctl net.inet6.ip6.forwarding"
	// TODO: define
	enableIPMasqueradingCMDFmt  = ""
	disableIPMasqueradingCMDFmt = ""
)

// TODO: implement
func EnableIPMasquerading(ifcName string) error {
	return nil
}

func DisableIPMasquerading(ifcName string) error {
	return nil
}

func AddRoute(ip, gateway, netmask string) error {
	if netmask == "" {
		return run("/sbin/route", "add", "-net", ip, gateway)
	}

	return run("/sbin/route", "add", "-net", ip, gateway, netmask)
}

func DeleteRoute(ip, gateway, netmask string) error {
	if netmask == "" {
		return run("/sbin/route", "delete", "-net", ip, gateway)
	}

	return run("/sbin/route", "delete", "-net", ip, gateway, netmask)
}

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{':'})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
