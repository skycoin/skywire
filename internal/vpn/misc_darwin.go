//+build darwin

package vpn

import (
	"bytes"
	"fmt"
)

const (
	gatewayForIfcCMDFmt     = "netstat -rn | grep default | grep %s | awk '{print $2}'"
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

func parseIPForwardingOutput(output []byte) (string, error) {
	output = bytes.TrimRight(output, "\n")

	outTokens := bytes.Split(output, []byte{':'})
	if len(outTokens) != 2 {
		return "", fmt.Errorf("invalid output: %s", output)
	}

	return string(bytes.Trim(outTokens[1], " ")), nil
}
