//go:build !tinygo

// Package netutil pkg/netutil/net_native.go c0-com-util
//
// Network-interface enumeration + the ipinfo.io HTTP probe. Split out of net.go
// behind //go:build !tinygo because TinyGo's net.Interface has no Addrs()
// method and net/http doesn't compile on the TinyGo wasm (js) target. A TinyGo
// dmsg client needs none of these (it has no host NICs to enumerate); the
// stubs in net_tinygo.go satisfy any transitive linker reference. See
// docs/design/tinygo-dmsg-client.md.
package netutil

import (
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/wlynxg/anet"
)

// Interface enumeration goes through anet, not net, because Android 11+
// denies unprivileged processes the netlink RIB dump the standard library
// uses: net.Interfaces() there fails with "route ip+net: netlinkrib:
// permission denied" and every caller of these helpers breaks. anet reads
// the same data by other means on Android and is a straight pass-through to
// the standard library everywhere else.

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

	ifaces, err := anet.Interfaces()
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

		addrs, err := anet.InterfaceAddrsByInterface(&iface)
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

// LocalAddresses returns a list of all local addresses
func LocalAddresses() ([]string, error) {
	result := make([]string, 0)

	ifaces, err := anet.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		// Skip Docker/container bridge interfaces
		if IsVirtualInterface(iface.Name) {
			continue
		}

		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.IsGlobalUnicast() {
				result = append(result, ip.String())
			}
		}
	}

	return result, nil
}

// LocalProtocol check a condition to use dmsghttp or direct url
func LocalProtocol() bool {
	resp, err := http.Get("https://ipinfo.io/country")
	if err != nil {
		return false
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	if string(respBody)[:2] == "CN" {
		return true
	}
	return false
}
