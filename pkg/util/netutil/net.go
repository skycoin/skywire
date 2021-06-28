package netutil

import (
	"fmt"
	"net"
)

// LocalNetworkInterfaceIPs gets IPs of all local interfaces.
func LocalNetworkInterfaceIPs() ([]net.IP, error) {
	ips, _, err := localNetworkInterfaceIPs("")
	return ips, err
}

// NetworkInterfaceIPs gets IPs of network interface with name `name`.
func NetworkInterfaceIPs(name string) ([]net.IP, error) {
	_, ifcIPs, err := localNetworkInterfaceIPs(name)
	return ifcIPs, err
}

// localNetworkInterfaceIPs gets IPs of all local interfaces. Separately returns list of IPs
// of interface `ifcName`.
func localNetworkInterfaceIPs(ifcName string) ([]net.IP, []net.IP, error) {
	var ifcIPs []net.IP

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting network interfaces: %w", err)
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
			return nil, nil, fmt.Errorf("error getting addresses for interface %s: %w", iface.Name, err)
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

			if ifcName != "" && iface.Name == ifcName {
				ifcIPs = append(ifcIPs, ip)
			}
		}
	}

	return ips, ifcIPs, nil
}

// IsPublicIP returns true if the provided IP is public.
// Obtained from: https://stackoverflow.com/questions/41670155/get-public-ip-in-golang
func IsPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := IP.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return false
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return false
		case ip4[0] == 192 && ip4[1] == 168:
			return false
		default:
			return true
		}
	}
	return false
}

// DefaultNetworkInterfaceIPs returns IP addresses for the default network interface
func DefaultNetworkInterfaceIPs() ([]net.IP, error) {
	networkIfc, err := DefaultNetworkInterface()
	if err != nil {
		return nil, fmt.Errorf("failed to get default network interface: %w", err)
	}
	localIPs, err := NetworkInterfaceIPs(networkIfc)
	if err != nil {
		return nil, fmt.Errorf("failed to get IPs of %s: %w", networkIfc, err)
	}
	return localIPs, nil
}

// HasPublicIP returns true if this machine has at least one
// publically available IP address
func HasPublicIP() (bool, error) {
	localIPs, err := LocalNetworkInterfaceIPs()
	if err != nil {
		return false, err
	}
	for _, IP := range localIPs {
		if IsPublicIP(IP) {
			return true, nil
		}
	}
	return false, nil
}
