//go:build linux
// +build linux

// Package vpn pkg/vpn/mesh_client_linux.go c4-app-vpn
package vpn

import (
	"context"
	"fmt"
	"net"
	"strconv"

	miekgdns "github.com/miekg/dns"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
	"github.com/skycoin/skywire/pkg/vpnrouter/router7/dns"
)

// meshClientGateway is the vpn-client's local mesh gateway: it lets the host
// itself reach *.dmsg / *.skynet by name, the single-machine counterpart of the
// vpn-router's LAN gateway. Instead of a LAN DNS + PREROUTING REDIRECT it uses a
// LOOPBACK resolver + OUTPUT-chain REDIRECT so locally-generated traffic is
// intercepted. The mesh dial (cfg.MeshDial → app.Client) bypasses the tunnel —
// it rides the visor's own transports, which the client already exempts from the
// tun via its direct routes — so mesh names resolve even while all other traffic
// egresses the VPN.
//
// This is deliberately opt-in: hijacking the host's DNS + adding OUTPUT nat rules
// is more intrusive on a user's own machine than on a dedicated router. Off
// unless ClientConfig.MeshGateway is set.
type meshClientGateway struct {
	log   logrus.FieldLogger
	proxy net.Listener
	dnsU  *miekgdns.Server
	dnsT  *miekgdns.Server
	rules [][]string // nat/OUTPUT argv sets we added (for teardown)
}

// meshResolverUpstreams mirrors the fixed upstreams pkg/vpnrouter/router7/dns
// forwards non-mesh queries to. The OUTPUT DNS-REDIRECT must EXEMPT these, or the
// resolver's own upstream queries would be redirected back into it — an infinite
// loop. Kept in sync with dns.NewServer's defaults.
var meshResolverUpstreams = []string{"8.8.8.8", "8.8.4.4"}

// startMeshClientGateway stands up the loopback resolver + transparent proxy and
// installs the OUTPUT nat rules. Runs until ctx is canceled; call stop() to tear
// the rules down. Requires root (nat table + low-port bind exemptions).
func startMeshClientGateway(ctx context.Context, cfg ClientConfig, log logrus.FieldLogger) (*meshClientGateway, error) {
	if log == nil {
		log = logrus.New()
	}
	cidr := cfg.MeshGatewayCIDR
	if cidr == "" {
		cidr = defaultMeshGatewayCIDR
	}
	gw, err := meshgw.New(cfg.MeshDial, cidr, cfg.MeshAliases, log)
	if err != nil {
		return nil, fmt.Errorf("mesh gateway: %w", err)
	}
	if cfg.MeshTLSMinter != nil {
		gw.EnableTLSMITM(cfg.MeshTLSMinter, 443)
		log.Info("mesh gateway: TLS-MITM on (HTTPS to *.dmsg/*.skynet; host must trust the CA)")
	}

	m := &meshClientGateway{log: log}

	// Transparent proxy on an ephemeral loopback port.
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("mesh gateway proxy listen: %w", err)
	}
	m.proxy = proxy
	proxyPort := proxy.Addr().(*net.TCPAddr).Port
	go func() {
		if serr := gw.ServeTransparent(ctx, proxy); serr != nil && ctx.Err() == nil {
			log.WithError(serr).Error("mesh gateway transparent proxy exited")
		}
	}()

	// Loopback resolver: mesh zones on top of router7's upstream-forwarding DNS.
	// Bind UDP on an ephemeral port, then TCP on the SAME number (separate
	// protocol namespaces) so one REDIRECT port covers both.
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		_ = proxy.Close() //nolint:errcheck
		return nil, fmt.Errorf("mesh gateway dns udp listen: %w", err)
	}
	dnsPort := udpConn.LocalAddr().(*net.UDPAddr).Port
	tcpLis, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(dnsPort))
	if err != nil {
		_ = proxy.Close()   //nolint:errcheck
		_ = udpConn.Close() //nolint:errcheck
		return nil, fmt.Errorf("mesh gateway dns tcp listen: %w", err)
	}
	resolver := dns.NewServer("127.0.0.1:"+strconv.Itoa(dnsPort), "local")
	gw.InstallDNS(resolver.Mux)
	m.dnsU = &miekgdns.Server{PacketConn: udpConn, Handler: resolver.Mux}
	m.dnsT = &miekgdns.Server{Listener: tcpLis, Handler: resolver.Mux}
	go func() {
		if serr := m.dnsU.ActivateAndServe(); serr != nil && ctx.Err() == nil {
			log.WithError(serr).Debug("mesh gateway dns (udp) exited")
		}
	}()
	go func() {
		if serr := m.dnsT.ActivateAndServe(); serr != nil && ctx.Err() == nil {
			log.WithError(serr).Debug("mesh gateway dns (tcp) exited")
		}
	}()

	if err := m.installRules(gw.PoolCIDR().String(), dnsPort, proxyPort); err != nil {
		m.stop()
		return nil, err
	}
	log.Infof("mesh gateway (client): *.dmsg/*.skynet → synthetic %s → proxy :%d; DNS via loopback :%d",
		gw.PoolCIDR(), proxyPort, dnsPort)
	return m, nil
}

// installRules adds the nat/OUTPUT REDIRECTs: DNS → the loopback resolver (with
// the resolver's own upstreams exempted to avoid a loop) and synthetic-pool TCP
// → the transparent proxy.
func (m *meshClientGateway) installRules(pool string, dnsPort, proxyPort int) error {
	var rules [][]string
	// Exempt the resolver's upstream queries FIRST (RETURN before the REDIRECT).
	for _, up := range meshResolverUpstreams {
		for _, proto := range []string{"udp", "tcp"} {
			rules = append(rules, []string{
				"-t", "nat", "-A", "OUTPUT", "-p", proto, "-d", up, "--dport", "53", "-j", "RETURN",
			})
		}
	}
	// Redirect all other DNS to the loopback resolver.
	for _, proto := range []string{"udp", "tcp"} {
		rules = append(rules, []string{
			"-t", "nat", "-A", "OUTPUT", "-p", proto, "--dport", "53",
			"-j", "REDIRECT", "--to-ports", strconv.Itoa(dnsPort),
		})
	}
	// Redirect synthetic-pool TCP to the transparent proxy.
	rules = append(rules, []string{
		"-t", "nat", "-A", "OUTPUT", "-p", "tcp", "-d", pool,
		"-j", "REDIRECT", "--to-ports", strconv.Itoa(proxyPort),
	})
	for _, rule := range rules {
		if err := osutil.RunElevated("iptables", rule...); err != nil {
			return fmt.Errorf("mesh gateway OUTPUT rule %v: %w", rule, err)
		}
		m.rules = append(m.rules, rule)
	}
	return nil
}

// stop tears down the nat rules and closes the resolver + proxy. Best-effort.
func (m *meshClientGateway) stop() {
	for _, rule := range m.rules {
		del := append([]string{"-t", "nat", "-D"}, rule[3:]...) // rule[3:] starts at "OUTPUT"
		if err := osutil.RunElevated("iptables", del...); err != nil {
			m.log.WithError(err).Warnf("mesh gateway teardown: remove rule %v", rule)
		}
	}
	m.rules = nil
	if m.dnsU != nil {
		_ = m.dnsU.Shutdown() //nolint:errcheck
	}
	if m.dnsT != nil {
		_ = m.dnsT.Shutdown() //nolint:errcheck
	}
	if m.proxy != nil {
		_ = m.proxy.Close() //nolint:errcheck
	}
}
