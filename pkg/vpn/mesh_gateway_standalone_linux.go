//go:build linux
// +build linux

// Package vpn pkg/vpn/mesh_gateway_standalone_linux.go c4-app-vpn
//
// Standalone mesh gateway: the mesh-name resolver + transparent proxy of the
// full vpn-router (setupMeshGateway), but WITHOUT taking over the LAN. It runs
// no DHCP, assigns no address, brings up no tunnel — it only serves `.dmsg` /
// `.skynet` DNS and REDIRECTs synthetic-pool TCP into the mesh.
//
// This is the "board behind your existing router" topology: a small appliance
// on the LAN gives every other device transparent, zero-per-device access to
// dmsg/skynet by name, while the household's own OpenWRT / DD-WRT router keeps
// doing DHCP and the default route. Two router-side settings wire it up:
//
//   - DNS forward:  `.dmsg` / `.skynet` queries → this board's DNS (below).
//   - static route: the synthetic pool (default 100.64.0.0/16) → this board.
//
// A LAN client then resolves `reward.dmsg` via the router → this board's DNS →
// a synthetic IP; its packets to that IP are static-routed here; PREROUTING
// REDIRECT lands them on the transparent proxy, which reads SO_ORIGINAL_DST and
// dials the encoded PK over the mesh (bypassing any VPN tunnel). Same mechanism
// as setupMeshGateway, minus the router role — see meshgw and router_linux.go.
package vpn

import (
	"context"
	"fmt"
	"net"
	"strconv"

	miekgdns "github.com/miekg/dns"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// defaultMeshGatewayDNSPort is the port the standalone gateway's DNS listens on.
// The upstream router forwards `.dmsg` / `.skynet` queries here; 53 is the
// natural default (dnsmasq's `server=/dmsg/<board>` forwards to :53), but a
// board already running a resolver on 53 can pick another and tell the router.
const defaultMeshGatewayDNSPort = 53

// MeshGatewayConfig configures a standalone mesh gateway (RunMeshGatewayOnly).
type MeshGatewayConfig struct {
	// LANInterface is the interface LAN traffic arrives on — the `iif` the
	// PREROUTING REDIRECT matches. Required: the REDIRECT must be pinned to the
	// downstream link so it never catches the board's own upstream traffic.
	LANInterface string

	// BindIP is the address the DNS server and transparent proxy listen on.
	// Nil / unspecified = all interfaces (0.0.0.0). Set it to the board's LAN IP
	// to avoid exposing DNS on other interfaces.
	BindIP net.IP

	// DNSPort is the mesh-name DNS port. 0 = defaultMeshGatewayDNSPort (53).
	DNSPort int

	// CIDR is the synthetic-IP pool leased to `.dmsg` / `.skynet` names. Empty =
	// defaultMeshGatewayCIDR (100.64.0.0/16). This is the range the upstream
	// router must static-route to this board.
	CIDR string

	// MeshDial dials the mesh for a resolved (scheme, pk, port). Required — from
	// the vpn-router app it is vpn.MeshDialer(appClient).
	MeshDial meshgw.MeshDial

	// Aliases (optional) maps friendly names → PK for names that aren't a raw PK.
	Aliases map[string]cipher.PubKey

	// TLSMinter (optional) enables TLS-MITM for HTTPS to `.dmsg` / `.skynet`
	// (port 443). LAN clients must trust the minter's CA.
	TLSMinter skynetca.LeafMinter
}

func (c MeshGatewayConfig) dnsPort() int {
	if c.DNSPort == 0 {
		return defaultMeshGatewayDNSPort
	}
	return c.DNSPort
}

func (c MeshGatewayConfig) bindHost() string {
	if c.BindIP == nil {
		return "0.0.0.0"
	}
	return c.BindIP.String()
}

// RunMeshGatewayOnly runs a standalone mesh gateway until ctx is canceled, then
// tears its state (firewall REDIRECT + IPv4 forwarding) back down. It reuses the
// same firewall backend (iptables, or nftables under -tags nftfw) and meshgw
// engine as the full router; it just skips the DHCP/DNS-server-owns-the-LAN,
// interface, WiFi, and tunnel-NAT roles.
func RunMeshGatewayOnly(ctx context.Context, cfg MeshGatewayConfig, log logrus.FieldLogger) error {
	if log == nil {
		log = logrus.New()
	}
	if cfg.LANInterface == "" {
		return fmt.Errorf("mesh gateway: LAN interface is required (the REDIRECT iif)")
	}
	if cfg.MeshDial == nil {
		return fmt.Errorf("mesh gateway: MeshDial is required")
	}

	gw, err := meshgw.New(cfg.MeshDial, cfg.CIDR, cfg.Aliases, log)
	if err != nil {
		return fmt.Errorf("mesh gateway: %w", err)
	}
	if cfg.TLSMinter != nil {
		gw.EnableTLSMITM(cfg.TLSMinter, 443)
		log.Info("mesh gateway: TLS-MITM on (HTTPS to *.dmsg/*.skynet; clients must trust the CA)")
	}

	// Forwarded LAN traffic transits this board, so IPv4 forwarding must be on.
	// Save the prior value and restore it on teardown so we don't silently leave
	// a box forwarding that wasn't before.
	prevForwarding, ferr := netctl.GetIPv4Forwarding()
	if ferr != nil {
		log.WithError(ferr).Warn("mesh gateway: could not read net.ipv4.ip_forward (continuing)")
	} else if err := netctl.SetIPv4Forwarding("1"); err != nil {
		return fmt.Errorf("mesh gateway: enable IPv4 forwarding: %w", err)
	}
	defer func() {
		if ferr == nil && prevForwarding != "1" {
			if err := netctl.SetIPv4Forwarding(prevForwarding); err != nil {
				log.WithError(err).Warn("mesh gateway: could not restore net.ipv4.ip_forward")
			}
		}
	}()

	fw, err := newFirewall(log)
	if err != nil {
		return fmt.Errorf("mesh gateway: firewall backend: %w", err)
	}
	defer fw.teardown()

	// Transparent proxy: bind all interfaces on an ephemeral port; REDIRECT is
	// pinned to whatever we get. SO_ORIGINAL_DST recovers the synthetic IP
	// regardless of bind address.
	lis, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", "0"))
	if err != nil {
		return fmt.Errorf("mesh gateway: transparent proxy listen: %w", err)
	}
	defer lis.Close() //nolint:errcheck // best-effort close on shutdown
	port := lis.Addr().(*net.TCPAddr).Port
	go func() {
		if err := gw.ServeTransparent(ctx, lis); err != nil && ctx.Err() == nil {
			log.WithError(err).Error("mesh gateway transparent proxy exited")
		}
	}()

	if err := fw.redirectTCP(fwPrerouting, cfg.LANInterface, gw.PoolCIDR(), uint16(port)); err != nil { //nolint:gosec // ephemeral port fits uint16
		return fmt.Errorf("mesh gateway: REDIRECT: %w", err)
	}

	// DNS: a bare mux with only the `.dmsg` / `.skynet` zones. Everything else is
	// REFUSED — this resolver is meant to sit BEHIND the LAN's real resolver,
	// which forwards just those two zones here.
	mux := miekgdns.NewServeMux()
	gw.InstallDNS(mux)
	dnsAddr := net.JoinHostPort(cfg.bindHost(), strconv.Itoa(cfg.dnsPort()))
	dnsErr := make(chan error, 2)
	for _, network := range []string{"udp", "tcp"} {
		s := &miekgdns.Server{Addr: dnsAddr, Net: network, Handler: mux}
		go func() { dnsErr <- s.ListenAndServe() }()
		go func() { <-ctx.Done(); _ = s.Shutdown() }() //nolint:errcheck // best-effort shutdown
	}

	log.Infof("mesh gateway (standalone): DNS %s serves *.dmsg/*.skynet → synthetic %s → proxy :%d (dials over mesh)",
		dnsAddr, gw.PoolCIDR().String(), port)
	log.Infof("mesh gateway: on the LAN router, forward .dmsg/.skynet DNS to %s:%d and static-route %s to this board",
		cfg.bindHost(), cfg.dnsPort(), gw.PoolCIDR().String())

	select {
	case <-ctx.Done():
		log.Info("mesh gateway shutting down")
		return nil
	case err := <-dnsErr:
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("mesh gateway: DNS server exited: %w", err)
	}
}
