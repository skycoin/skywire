//go:build linux
// +build linux

// Package netutil pkg/netutil/net_linux.go c0-com-util
package netutil

import (
	"bufio"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
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
	if name, err := interfaceOfDefaultRoute(); err == nil && name != "" {
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
