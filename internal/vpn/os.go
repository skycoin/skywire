package vpn

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ip, netmask, gateway string, mtu int) error {
	return run("ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

// NetworkInterfaceGateway gets gateway of the network interface with name `ifcName`.
func NetworkInterfaceGateway(ifcName string) (net.IP, error) {
	cmd := fmt.Sprintf(gatewayForIfcCMDFmt, ifcName)
	outBytes, err := exec.Command("sh", "-c", cmd).Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("error running command %s: %w", cmd, err)
	}

	outBytes = bytes.TrimRight(outBytes, "\n")

	outLines := bytes.Split(outBytes, []byte{'\n'})

	for _, l := range outLines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			// initially look for IPv4 address
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, fmt.Errorf("couldn't find gateway IP for \"%s\"", ifcName)
}

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	defaultNetworkIfcName, err := DefaultNetworkInterface()
	if err != nil {
		return nil, fmt.Errorf("error getting default network interface name: %w", err)
	}

	return NetworkInterfaceGateway(defaultNetworkIfcName)
}

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("error getting network interfaces: %w", err)
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", fmt.Errorf("error getting addresses for interface %s: %w", iface.Name, err)
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue // not an ipv4 address
			}

			return iface.Name, nil
		}
	}

	return "", errors.New("no internet connection")
}

// LocalNetworkInterfaceIPs gets IPs of all local interfaces.
func LocalNetworkInterfaceIPs() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("error getting network interfaces: %w", err)
	}

	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("error getting addresses for interface %s: %w", iface.Name, err)
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue // not an ipv4 address
			}

			ips = append(ips, ip)
		}
	}

	return ips, nil
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
