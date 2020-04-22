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

func SetupTUN(ifcName, ip, netmask, gateway string, mtu int) error {
	return run("/sbin/ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

func GatewayIP(ifcName string) (net.IP, error) {
	cmd := fmt.Sprintf(gatewayForIfcCMDFmt, ifcName)
	outBytes, err := exec.Command("/bin/bash", "-c", cmd).Output()
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

func DefaultGatewayIP() (net.IP, error) {
	defaultNetworkIfcName, err := DefaultNetworkIfc()
	if err != nil {
		return nil, fmt.Errorf("error getting default network interface name: %w", err)
	}

	return GatewayIP(defaultNetworkIfcName)
}

func DefaultNetworkIfc() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
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
			return "", err
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

func GetIPsToReserve() ([]net.IP, error) {
	var toReserve []net.IP

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
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
			return nil, err
		}
		for _, addr := range addrs {
			fmt.Printf("Scanning addr: %s\n", addr)
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

			toReserve = append(toReserve, ip)
		}
	}

	return toReserve, nil
}

func GetIPv4ForwardingValue() (string, error) {
	return getIPForwardingValue(getIPv4ForwardingCMD)
}

func GetIPv6ForwardingValue() (string, error) {
	return getIPForwardingValue(getIPv6ForwardingCMD)
}

func SetIPv4ForwardingValue(val string) error {
	cmd := fmt.Sprintf(setIPv4ForwardingCMDFmt, val)
	if err := exec.Command("/bin/bash", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

func SetIPv6ForwardingValue(val string) error {
	cmd := fmt.Sprintf(setIPv6ForwardingCMDFmt, val)
	if err := exec.Command("/bin/bash", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", cmd, err)
	}

	return nil
}

func EnableIPv4Forwarding() error {
	return SetIPv4ForwardingValue("1")
}

func EnableIPv6Forwarding() error {
	return SetIPv6ForwardingValue("1")
}

func getIPForwardingValue(cmd string) (string, error) {
	outBytes, err := exec.Command("/bin/bash", "-c", cmd).Output()
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
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running command %s: %w", bin, err)
	}

	return nil
}
