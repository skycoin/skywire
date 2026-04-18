// Package visor pkg/visor/init_skynet_ports.go
//
// Auto-registers the visor's localhost service ports for skynet
// forwarding so they're accessible via .skynet URLs (or any skynet
// client) without manual RegisterTCPPort calls.
//
// Which ports: everything that already listens on localhost and
// would be useful to reach remotely. The sky-forwarding server
// (port 57) validates each forwarded port against this list, so
// only explicitly registered ports are reachable — the access
// control model is the same as DMSG (PK-authenticated route +
// noise handshake) just over the routing mesh instead of dmsg
// servers.
//
// Ports that serve ONLY over DMSG (no localhost listener) like
// port 136 (route setup RPC) are NOT registered here because
// there's nothing on localhost to forward to. Making those
// skynet-accessible would require also binding them on localhost
// — a separate, larger change.
package visor

import (
	"context"
	"net"
	"strconv"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// initSkynetForwardPorts registers the visor's localhost service
// ports for skynet forwarding. Called after all services are started
// so the ports are actually listening when we probe them.
func initSkynetForwardPorts(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil {
		return nil
	}

	// Collect candidate ports from the visor config. Each entry is
	// (label, addr-string) where addr-string may be "host:port",
	// ":port", or "port". We extract the port number and skip if
	// it's empty or zero.
	// RPC (cli_addr) and hypervisor (http_addr) are NOT registered
	// here — they must not be reachable over dmsg or skynet at all.
	// Exposing them would allow noise-handshake spamming attacks
	// and unauthed RPC access. They stay localhost-only.
	//
	// Only register ports for public-facing services that are safe
	// to serve to any PK-authenticated peer.
	// The log server is already served on DMSG/skynet port 80 via
	// the service registry — no need to also forward its localhost
	// TCP port. Only register ports that aren't already served
	// directly on DMSG.
	candidates := []struct {
		label string
		addr  string
	}{}
	if ui := v.conf.UIServer; ui != nil && ui.Enable {
		candidates = append(candidates, struct {
			label string
			addr  string
		}{"ui_server", ui.LocalAddr})
	}
	// Skychat, skysocks, and other apps bind their own localhost
	// ports dynamically. Those are registered separately by the app
	// framework when they start — see appserver's port registration.
	// We only handle visor-level services here.

	// Register directly into the allowed-ports map instead of going
	// through RegisterTCPPort, which dials the port to check if
	// something is listening. At init time some services (hypervisor)
	// may not be up yet — but the sky-forwarding handler re-checks
	// availability at connection time, so pre-registering is safe.
	v.allowed.mu.Lock()
	registered := 0
	for _, c := range candidates {
		port := extractPort(c.addr)
		if port <= 0 {
			continue
		}
		if v.allowed.ports[port] {
			continue // already registered
		}
		v.allowed.ports[port] = true
		registered++
		log.WithField("port", port).WithField("service", c.label).
			Info("Registered port for skynet forwarding")
	}
	v.allowed.mu.Unlock()
	if registered > 0 {
		log.WithField("count", registered).
			Info("Skynet-forwardable ports registered; accessible via .skynet URLs")
	}
	return nil
}

// extractPort pulls the numeric port from an address string.
// Handles "host:port", ":port", and bare "port".
func extractPort(addr string) int {
	if addr == "" {
		return 0
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Maybe bare port number?
		portStr = addr
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}
