//go:build android

// Package netutil pkg/netutil/dns_android.go c0-com-util
package netutil

import (
	"context"
	"net"
	"os"
	"sync/atomic"
)

// Android has no /etc/resolv.conf, so a pure-Go binary asks [::1]:53 and every
// name fails. Ask the network's resolvers instead, which the host app reports.
func init() {
	path := os.Getenv(DNSServersFileEnv)
	var next atomic.Uint32
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		// Each dial takes the next server: a UDP dial cannot fail on a dead one,
		// so this is what moves a retry after a timeout along the list.
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			servers := dnsServers(path)
			server := servers[int(next.Add(1)-1)%len(servers)]
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		},
	}
}
