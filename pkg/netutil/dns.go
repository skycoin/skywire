// Package netutil pkg/netutil/dns.go c0-com-util
package netutil

import (
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
)

// DNSServersFileEnv names the file where the Android host app keeps the current
// network's resolvers, one address per line. See dns_android.go.
const DNSServersFileEnv = "SKYWIRE_DNS_SERVERS_FILE"

// fallbackDNSServers answer when the host app has reported no resolver.
var fallbackDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// dnsServers lists the resolvers to ask: those in the file, then the fallbacks.
func dnsServers(path string) []string {
	var servers []string
	if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // path comes from the host app
		for line := range strings.Lines(string(raw)) {
			addr, err := netip.ParseAddr(strings.TrimSpace(line))
			if err != nil {
				continue
			}
			server := net.JoinHostPort(addr.String(), "53")
			if !slices.Contains(servers, server) {
				servers = append(servers, server)
			}
		}
	}
	for _, server := range fallbackDNSServers {
		if !slices.Contains(servers, server) {
			servers = append(servers, server)
		}
	}
	return servers
}
