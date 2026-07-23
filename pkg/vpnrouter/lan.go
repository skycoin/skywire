//go:build linux

// Package vpnrouter pkg/vpnrouter/lan.go c4-app-vpn
//
// StartLAN runs an embedded, pure-Go DHCPv4 + DNS server for the vpn-router's
// downstream (LAN/WiFi) interface, replacing the external dnsmasq the app used
// to shell out to. Built on the vendored router7 handler libraries
// (pkg/vpnrouter/router7) — see that package's doc.go for provenance.
package vpnrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/google/renameio"
	krolaw "github.com/krolaw/dhcp4"
	"github.com/krolaw/dhcp4/conn"
	miekgdns "github.com/miekg/dns"

	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
	"github.com/skycoin/skywire/pkg/vpnrouter/router7/dhcp4d"
	"github.com/skycoin/skywire/pkg/vpnrouter/router7/dns"
)

// StartLAN serves DHCPv4 (:67) and DNS (:53) on ifaceName — the vpn-router's
// downstream interface — in-process, replacing dnsmasq. The gateway address is
// the interface's own IPv4 (assigned by the app's setupLANInterface); clients
// are handed addresses starting at gateway+1 and given the gateway as their
// router and DNS server. LAN hostnames resolve from the live DHCP leases, which
// persist under permDir/dhcp4d/leases.json across restarts. Blocks until ctx is
// canceled or a server exits with an error. Requires CAP_NET_* / root.
//
// When gw is non-nil the MESH GATEWAY is layered on: gw's `.dmsg` / `.skynet`
// zone handlers are registered on the DNS mux so clients resolve mesh names to
// synthetic IPs (the transparent proxy that dials them is started separately by
// the caller). gw nil = plain LAN DNS only.
func StartLAN(ctx context.Context, ifaceName, permDir string, gw *meshgw.Gateway) error {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return fmt.Errorf("lan interface %q: %w", ifaceName, err)
	}
	serverIP, err := firstIPv4(iface)
	if err != nil {
		return fmt.Errorf("lan interface %q has no IPv4 (assign the gateway address first): %w", ifaceName, err)
	}
	if err := os.MkdirAll(filepath.Join(permDir, "dhcp4d"), 0o750); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	leasesPath := filepath.Join(permDir, "dhcp4d", "leases.json")

	handler, err := dhcp4d.NewHandler(permDir, iface, ifaceName, nil)
	if err != nil {
		return fmt.Errorf("dhcp handler: %w", err)
	}
	dnsSrv := dns.NewServer(serverIP.String()+":53", "lan")
	if gw != nil {
		gw.InstallDNS(dnsSrv.Mux)
	}

	// Load persisted leases into both the DHCP handler (so clients keep their
	// address across a restart) and the DNS server (so their hostnames resolve).
	if b, rerr := os.ReadFile(leasesPath); rerr == nil { //nolint:gosec // leasesPath is our own state path
		var persisted []*dhcp4d.Lease
		if json.Unmarshal(b, &persisted) == nil {
			handler.SetLeases(persisted)
			dnsSrv.SetLeases(derefLeases(persisted))
		}
	}
	// On every lease change: persist atomically + refresh DNS resolution.
	handler.Leases = func(newLeases []*dhcp4d.Lease, _ *dhcp4d.Lease) {
		if b, merr := json.Marshal(newLeases); merr == nil {
			_ = renameio.WriteFile(leasesPath, b, 0o644) //nolint:errcheck // best-effort persist
		}
		dnsSrv.SetLeases(derefLeases(newLeases))
	}

	errc := make(chan error, 3)

	// DHCPv4: a UDP :67 listener bound to the LAN interface + the krolaw serve loop.
	dc, err := conn.NewUDP4BoundListener(ifaceName, ":67")
	if err != nil {
		return fmt.Errorf("dhcp listener on %q: %w", ifaceName, err)
	}
	go func() { errc <- krolaw.Serve(dc, handler) }()
	go func() { <-ctx.Done(); _ = dc.Close() }() //nolint:errcheck // best-effort close on shutdown

	// DNS: UDP + TCP :53 bound to the gateway address, served by the router7 mux.
	for _, network := range []string{"udp", "tcp"} {
		s := &miekgdns.Server{Addr: serverIP.String() + ":53", Net: network, Handler: dnsSrv.Mux}
		go func() { errc <- s.ListenAndServe() }()
		go func() { <-ctx.Done(); _ = s.Shutdown() }() //nolint:errcheck // best-effort shutdown
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

// firstIPv4 returns the first IPv4 address assigned to iface.
func firstIPv4(iface *net.Interface) (net.IP, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if v4 := ipn.IP.To4(); v4 != nil {
				return v4, nil
			}
		}
	}
	return nil, fmt.Errorf("no IPv4 address on %s", iface.Name)
}

// derefLeases adapts the DHCP handler's []*Lease to the DNS server's []Lease.
func derefLeases(in []*dhcp4d.Lease) []dhcp4d.Lease {
	out := make([]dhcp4d.Lease, 0, len(in))
	for _, l := range in {
		if l != nil {
			out = append(out, *l)
		}
	}
	return out
}
