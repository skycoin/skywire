//go:build linux
// +build linux

// Package netutil pkg/netutil/net_linux.go c0-com-util
package netutil

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/wlynxg/anet"
)

// DefaultNetworkInterface fetches default network interface name: the
// interface of each IPv4 default route, one a line, as `ip r` lists them.
//
// Where the routing table cannot be read, as by an Android app, it is the
// interface the kernel routes a packet to the internet through, then the first
// usable interface. When none qualifies (airplane mode) it returns "" without
// error, which callers handle as unknown.
func DefaultNetworkInterface() (string, error) {
	if runtime.GOOS != "android" {
		if f, err := os.Open("/proc/net/route"); err == nil {
			ifcs := defaultRouteInterfaces(f)
			f.Close() //nolint:errcheck,gosec // read-only
			if len(ifcs) > 0 {
				return strings.Join(ifcs, "\n"), nil
			}
		}
	}
	if name := routedInterface(); name != "" {
		return name, nil
	}
	return firstRoutableInterface()
}

// defaultRouteInterfaces reads a /proc/net/route table and returns the
// interfaces of its default routes that are up, lowest metric first.
func defaultRouteInterfaces(r io.Reader) []string {
	const rtfUp = 0x1
	type route struct {
		ifc    string
		metric int
	}
	var routes []route
	s := bufio.NewScanner(r)
	s.Scan() // the header
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&rtfUp == 0 {
			continue
		}
		metric, _ := strconv.Atoi(f[6]) //nolint:errcheck // 0 sorts first, as it would
		routes = append(routes, route{f[0], metric})
	}
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].metric < routes[j].metric })
	var ifcs []string
	seen := map[string]bool{}
	for _, rt := range routes {
		if !seen[rt.ifc] {
			seen[rt.ifc] = true
			ifcs = append(ifcs, rt.ifc)
		}
	}
	return ifcs
}

// routedInterface is the interface the kernel would send a packet to the
// internet through, or "". Connecting a UDP socket looks the route up and
// sends nothing.
func routedInterface() string {
	c, err := net.Dial("udp4", "198.51.100.1:9") // TEST-NET-2, never answered
	if err != nil {
		return ""
	}
	defer c.Close() //nolint:errcheck
	local, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	ifaces, err := anet.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(local.IP) {
				return iface.Name
			}
		}
	}
	return ""
}

func firstRoutableInterface() (string, error) {
	ifaces, err := anet.Interfaces()
	if err != nil {
		return "", fmt.Errorf("error getting network interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
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
			if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() {
				return iface.Name, nil
			}
		}
	}
	return "", nil
}
