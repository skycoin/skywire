//+build linux

package vpn

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"

	"github.com/syndtr/gocapability/capability"
	"golang.org/x/sys/unix"
)

const (
	defaultNetworkInterfaceCMD  = "ip addr | awk '/state UP/ {print $2}' | sed 's/.$//'"
	getIPv4ForwardingCMD        = "sysctl net.ipv4.ip_forward"
	getIPv6ForwardingCMD        = "sysctl net.ipv6.conf.all.forwarding"
	setIPv4ForwardingCMDFmt     = "sysctl -w net.ipv4.ip_forward=%s"
	setIPv6ForwardingCMDFmt     = "sysctl -w net.ipv6.conf.all.forwarding=%s"
	enableIPMasqueradingCMDFmt  = "iptables -t nat -A POSTROUTING -o %s -j MASQUERADE"
	disableIPMasqueradingCMDFmt = "iptables -t nat -D POSTROUTING -o %s -j MASQUERADE"
)

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	outputBytes, err := exec.Command("sh", "-c", defaultNetworkInterfaceCMD).Output()
	if err != nil {
		return "", fmt.Errorf("error running command %s: %w", defaultNetworkInterfaceCMD, err)
	}

	outputBytes = bytes.TrimRight(outputBytes, "\n")

	lines := bytes.Split(outputBytes, []byte{'\n'})
	// take only first one, should be enough in most cases
	return string(lines[0]), nil
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
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil { //nolint:gosec
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// SetIPv6ForwardingValue sets `val` value of IPv6 forwarding.
func SetIPv6ForwardingValue(val string) error {
	cmd := fmt.Sprintf(setIPv6ForwardingCMDFmt, val)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil { //nolint:gosec
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
	//nolint:gosec
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

// DisableIPMasquerading disables IP masquerading for the interface with name `ifcName`.
func DisableIPMasquerading(ifcName string) error {
	cmd := fmt.Sprintf(disableIPMasqueradingCMDFmt, ifcName)
	//nolint:gosec
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

func getIPForwardingValue(cmd string) (string, error) {
	outBytes, err := exec.Command("sh", "-c", cmd).Output() //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("error running command %s: %w", cmd, err)
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

var setupServerOnce sync.Once

func setupServerSysPrivileges() (suid int, err error) {
	setupServerOnce.Do(func() {
		var caps capability.Capabilities

		caps, err = capability.NewPid2(0)
		if err != nil {
			err = fmt.Errorf("failed to init capabilities: %w", err)
			return
		}

		err = caps.Load()
		if err != nil {
			err = fmt.Errorf("failed to load capabilities: %w", err)
			return
		}

		// set `CAP_NET_ADMIN` capability to needed caps sets.
		caps.Set(capability.CAPS|capability.BOUNDS|capability.AMBIENT, capability.CAP_NET_ADMIN, capability.CAP_NET_RAW,
			capability.CAP_DAC_READ_SEARCH, capability.CAP_NET_BIND_SERVICE)
		if err := caps.Apply(capability.CAPS | capability.BOUNDS | capability.AMBIENT); err != nil {
			err = fmt.Errorf("failed to apply capabilties: %w", err)
			return
		}

		// let child process keep caps sets from the parent, so we may do calls to
		// system utilities with these caps.
		if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0); err != nil {
			err = fmt.Errorf("failed to set PR_SET_KEEPCAPS: %w", err)
			return
		}
	})

	return 0, nil
}

func releaseServerSysPrivileges(_ int) error {
	return nil
}
