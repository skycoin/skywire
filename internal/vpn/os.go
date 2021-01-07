package vpn

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

func parseCIDR(ipCIDR string) (ipStr, netmask string, err error) {
	ip, net, err := net.ParseCIDR(ipCIDR)
	if err != nil {
		return "", "", err
	}

	return ip.String(), fmt.Sprintf("%d.%d.%d.%d", net.Mask[0], net.Mask[1], net.Mask[2], net.Mask[3]), nil
}
