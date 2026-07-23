//go:build linux
// +build linux

// Package vpn pkg/vpn/router_linux.go c4-app-vpn
package vpn

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
	"github.com/skycoin/skywire/pkg/vpnrouter"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// Router orchestrates the downstream interface, the DHCP/DNS + optional WiFi AP
// daemons, and the forwarding/NAT into the mesh-VPN tunnel. Construct with
// NewRouter, then Run.
type Router struct {
	cfg RouterConfig
	log logrus.FieldLogger

	confDir string
	daemons []*exec.Cmd
	tunIfc  string

	// mesh gateway (optional; nil cfg.MeshDial = off).
	meshGW       *meshgw.Gateway
	meshProxy    net.Listener
	meshRedirect []string // the nat/PREROUTING REDIRECT argv we added (nil = none)

	// state captured for restoration on teardown.
	prevForwarding string
	masquerading   bool
	forwardRules   [][]string // iptables argv sets we added to the FORWARD chain
	policyRouting  bool       // true once the LAN→tun policy route+rule are installed
	mssClamped     bool       // true once the TCPMSS clamp rule is installed
}

// mssClampRule is the mangle-table rule that clamps forwarded TCP SYNs' MSS to
// the path MTU. Shared by setup + teardown ("-A" vs "-D") via mssClampArgs.
func (r *Router) mssClampArgs(op string) []string {
	return []string{
		"-t", "mangle", op, "FORWARD", "-o", r.tunIfc,
		"-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--clamp-mss-to-pmtu",
	}
}

// lanPolicyTable is the dedicated routing table the router uses to send
// downstream-LAN traffic through the tunnel. A high, fixed id keeps it clear of
// the main/local/default tables and of typical per-link tables; the router owns
// it exclusively and flushes it on teardown.
const lanPolicyTable = 142

// NewRouter validates cfg and returns a Router ready to Run. It does not touch
// any system state.
func NewRouter(cfg RouterConfig, log logrus.FieldLogger) (*Router, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = logrus.New()
	}
	return &Router{cfg: cfg, log: log}, nil
}

// Run sets the router up and blocks until ctx is canceled, then tears
// everything back down. It returns the setup error (after best-effort teardown)
// or nil on clean shutdown.
func (r *Router) Run(ctx context.Context) error {
	if err := r.setup(ctx); err != nil {
		r.teardown()
		return err
	}
	r.log.Infof("vpn-router up: %s (%s) → %s; clients get %s via DHCP",
		r.cfg.LANInterface, r.variant(), r.tunIfc, r.cfg.Subnet)
	<-ctx.Done()
	r.log.Info("vpn-router shutting down")
	r.teardown()
	return nil
}

func (r *Router) variant() string {
	if r.cfg.WiFi != nil {
		return "WiFi AP " + r.cfg.WiFi.SSID
	}
	return "ethernet"
}

func (r *Router) setup(ctx context.Context) error {
	if os.Geteuid() != 0 {
		// hostapd/dnsmasq + ip/iptables need privilege; the vpn apps run
		// elevated. Fail early with a clear message rather than partway through.
		r.log.Warn("vpn-router is not running as root — interface/daemon setup will likely fail")
	}

	dir, err := os.MkdirTemp("", "skywire-vpn-router-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	r.confDir = dir

	// 1. Bring up the downstream interface with the gateway address.
	if err := r.setupLANInterface(); err != nil {
		return err
	}

	// 2. WiFi-out: start hostapd so the interface actually beacons an AP.
	if r.cfg.WiFi != nil {
		if err := r.startHostapd(ctx); err != nil {
			return err
		}
	}

	// 3. Mesh gateway (optional): build it before StartLAN so its `.dmsg` /
	// `.skynet` DNS handlers are registered on the LAN resolver's mux, and start
	// its transparent proxy + REDIRECT so resolved synthetic IPs actually dial.
	if r.cfg.MeshDial != nil {
		if err := r.setupMeshGateway(ctx); err != nil {
			return err
		}
	}

	// 4. DHCP + DNS for the downstream subnet — an embedded, pure-Go engine
	// (vendored router7 dhcp4d + dns), replacing the external dnsmasq the app
	// used to shell out to. StartLAN blocks until ctx is canceled, so run it
	// in the background; a startup error surfaces via the log. r.meshGW (nil when
	// the mesh gateway is off) layers the mesh-name zones onto the resolver.
	go func() {
		if err := vpnrouter.StartLAN(ctx, r.cfg.LANInterface, r.confDir, r.meshGW); err != nil && ctx.Err() == nil {
			r.log.WithError(err).Error("embedded DHCP/DNS server exited")
		}
	}()

	// 5. Wait for the vpn-client tunnel, then forward + NAT into it.
	tun, err := r.awaitTUN(ctx)
	if err != nil {
		return err
	}
	r.tunIfc = tun
	return r.setupNAT()
}

// setupLANInterface flushes and assigns the gateway address to the downstream
// interface and brings it up.
func (r *Router) setupLANInterface() error {
	lan := r.cfg.LANInterface
	cidr := fmt.Sprintf("%s/%d", r.cfg.Gateway, r.cfg.prefixLen())
	// Flush any stale addressing, set ours, bring the link up.
	_ = netctl.FlushAddrs(lan) //nolint:errcheck // best-effort clean slate
	if err := netctl.AddrAdd(lan, cidr); err != nil {
		return fmt.Errorf("assign %s to %s: %w", cidr, lan, err)
	}
	if err := netctl.LinkUp(lan); err != nil {
		return fmt.Errorf("bring up %s: %w", lan, err)
	}
	r.log.Infof("downstream %s = %s", lan, cidr)
	return nil
}

func (r *Router) startHostapd(ctx context.Context) error {
	if _, err := exec.LookPath("hostapd"); err != nil {
		return fmt.Errorf("hostapd not found — install it for the WiFi-out variant: %w", err)
	}
	// Realtek SDIO radios (rtl8723bs on the original skyminer Orange Pi boards,
	// and relatives) have a firmware power-save bug that makes hostapd AP mode
	// flap (AP-ENABLED→DISABLED, stations deauth'd) unless the interface's
	// power management is off. Disabling it at runtime is the single most
	// effective stability fix; it's best-effort (non-Realtek radios don't need
	// it and `iw` may be absent, so a failure here is only logged). The
	// persistent counterpart — `options rtl8723bs rtw_power_mgnt=0
	// rtw_ips_mode=0` in /etc/modprobe.d — is documented in the router setup
	// guide for boards that reset PM on link-up.
	if err := osutil.RunElevated("iw", "dev", r.cfg.LANInterface, "set", "power_save", "off"); err != nil {
		r.log.WithError(err).Debugf("could not disable power_save on %s (harmless on non-Realtek radios)", r.cfg.LANInterface)
	}
	confPath := filepath.Join(r.confDir, "hostapd.conf")
	if err := os.WriteFile(confPath, []byte(r.cfg.WiFi.HostapdConf(r.cfg.LANInterface)), 0o600); err != nil {
		return fmt.Errorf("write hostapd.conf: %w", err)
	}
	return r.startDaemon(ctx, "hostapd", confPath)
}

// startDaemon launches a long-running child bound to ctx (killed on cancel) and
// records it for teardown.
func (r *Router) startDaemon(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // bin is a fixed daemon name, args are our generated conf paths
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	r.daemons = append(r.daemons, cmd)
	r.log.Infof("started %s (pid %d)", bin, cmd.Process.Pid)
	return nil
}

// awaitTUN resolves the configured tunnel interface, or polls for the first
// tun* interface to appear (vpn-client may start after the router).
func (r *Router) awaitTUN(ctx context.Context) (string, error) {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if name := r.findTUN(); name != "" {
			return name, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("no VPN tunnel interface appeared within 90s — is the vpn-client app running and connected?")
		case <-tick.C:
		}
	}
}

// findTUN returns the configured tunnel interface if it exists, else the first
// tun* interface, else "".
func (r *Router) findTUN() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	if want := r.cfg.TUNInterface; want != "" {
		for _, ifc := range ifaces {
			if ifc.Name == want {
				return want
			}
		}
		return ""
	}
	for _, ifc := range ifaces {
		if strings.HasPrefix(ifc.Name, "tun") {
			return ifc.Name
		}
	}
	return ""
}

// setupNAT enables IPv4 forwarding and masquerades the downstream subnet out the
// tunnel, with targeted FORWARD rules downstream↔tunnel.
func (r *Router) setupNAT() error {
	prev, err := GetIPv4ForwardingValue()
	if err == nil {
		r.prevForwarding = prev
	}
	if err := EnableIPv4Forwarding(); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	if err := EnableIPMasquerading(r.tunIfc); err != nil {
		return fmt.Errorf("masquerade out %s: %w", r.tunIfc, err)
	}
	r.masquerading = true

	// downstream → tunnel (new+established) and tunnel → downstream (established)
	rules := [][]string{
		{"-A", "FORWARD", "-i", r.cfg.LANInterface, "-o", r.tunIfc, "-j", "ACCEPT"},
		{"-A", "FORWARD", "-i", r.tunIfc, "-o", r.cfg.LANInterface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
	}
	for _, rule := range rules {
		if err := osutil.RunElevated("iptables", rule...); err != nil {
			return fmt.Errorf("iptables %s: %w", strings.Join(rule, " "), err)
		}
		r.forwardRules = append(r.forwardRules, rule)
	}
	r.log.Infof("NAT: %s → %s (masquerade)", r.cfg.LANInterface, r.tunIfc)

	// Clamp forwarded TCP MSS to the path MTU. The mesh tunnel's usable MTU is
	// below the LAN's 1500, so without this a downstream client's full-size
	// segments black-hole (PMTU discovery is unreliable across NAT) — TCP
	// stalls while ping works. Clamping lets clients auto-negotiate a fitting
	// MSS with no per-client MTU tweaks.
	if err := osutil.RunElevated("iptables", r.mssClampArgs("-A")...); err != nil {
		// Non-fatal: forwarding still works; large-segment flows may stall.
		r.log.WithError(err).Warn("could not install TCPMSS clamp (large downstream TCP flows may stall)")
	} else {
		r.mssClamped = true
	}

	if err := r.setupPolicyRouting(); err != nil {
		return err
	}
	return nil
}

// setupPolicyRouting sends downstream-LAN traffic through the tunnel without
// touching the router's own default route.
//
// This is the difference between a VPN *client* and a VPN *router*: a client
// can point the whole host's default route at the tun, but a router MUST keep
// its own uplink — the mesh transport carrying the tunnel to the vpn-server
// rides that uplink, so redirecting the host default through the tun would cut
// the tunnel's own carrier. Instead we install a dedicated table with a
// default route out the tun and an `ip rule` matching traffic FORWARDED IN from
// the downstream interface, so client packets egress via the VPN while
// everything the router originates (dmsg/skywire included) keeps the main table.
//
// The rule matches by input interface (`iif <lan>`), NOT by source subnet: the
// gateway's own IP is IN the LAN subnet, so a `from <subnet>` rule also caught
// the router's replies to clients (DNS answers, ICMP replies, source =
// gateway IP) and shoved them out the tun, black-holing every local service and
// gateway response. `iif <lan>` only matches packets forwarded in from the
// downstream — locally-generated router traffic has no iif and stays on main.
func (r *Router) setupPolicyRouting() error {
	lan := r.cfg.LANInterface

	// default route via the tun device in our private table.
	if err := netctl.ReplaceDefaultRouteDev(r.tunIfc, lanPolicyTable); err != nil {
		return fmt.Errorf("policy route (default dev %s table %d): %w", r.tunIfc, lanPolicyTable, err)
	}
	// route traffic forwarded in from the LAN interface into that table
	// (AddRuleIif deletes any stale copy first so a restart doesn't stack rules).
	if err := netctl.AddRuleIif(lan, lanPolicyTable); err != nil {
		return fmt.Errorf("policy rule (iif %s lookup %d): %w", lan, lanPolicyTable, err)
	}
	r.policyRouting = true
	r.log.Infof("policy routing: iif %s → %s (table %d); router uplink + local services unchanged", lan, r.tunIfc, lanPolicyTable)
	return nil
}

// setupMeshGateway turns the router into a full mesh gateway: it builds the
// meshgw.Gateway (DNS zones + synthetic-IP pool), starts its transparent proxy
// bound to the downstream gateway address on an ephemeral port, and installs a
// nat/PREROUTING REDIRECT so downstream TCP aimed at the synthetic pool lands on
// that proxy — which reads the original destination, maps the synthetic IP back
// to a (scheme, pubkey), and dials it over the mesh via cfg.MeshDial.
//
// Note the mesh path deliberately bypasses the VPN tunnel: the REDIRECT fires in
// PREROUTING before any routing decision, so pool traffic never reaches the
// LAN→tun policy route; and the proxy's own mesh dials originate locally (no
// iif), staying on the main table. Mesh services are reached over the visor's
// transports directly, not through the exit server.
func (r *Router) setupMeshGateway(ctx context.Context) error {
	gw, err := meshgw.New(r.cfg.MeshDial, r.cfg.MeshGatewayCIDR, r.cfg.MeshAliases, r.log)
	if err != nil {
		return fmt.Errorf("mesh gateway: %w", err)
	}
	if r.cfg.MeshTLSMinter != nil {
		gw.EnableTLSMITM(r.cfg.MeshTLSMinter, 443)
		r.log.Info("mesh gateway: TLS-MITM on (HTTPS to *.dmsg/*.skynet; clients must trust the CA)")
	}
	r.meshGW = gw

	// Transparent proxy on the gateway address, ephemeral port (no fixed-port
	// collision; the REDIRECT rule below is pinned to whatever we get).
	lis, err := net.Listen("tcp", net.JoinHostPort(r.cfg.Gateway.String(), "0"))
	if err != nil {
		return fmt.Errorf("mesh gateway proxy listen: %w", err)
	}
	r.meshProxy = lis
	port := lis.Addr().(*net.TCPAddr).Port
	go func() {
		if err := gw.ServeTransparent(ctx, lis); err != nil && ctx.Err() == nil {
			r.log.WithError(err).Error("mesh gateway transparent proxy exited")
		}
	}()

	rule := []string{
		"-t", "nat", "-A", "PREROUTING",
		"-i", r.cfg.LANInterface,
		"-d", gw.PoolCIDR().String(),
		"-p", "tcp",
		"-j", "REDIRECT", "--to-ports", strconv.Itoa(port),
	}
	if err := osutil.RunElevated("iptables", rule...); err != nil {
		return fmt.Errorf("mesh gateway REDIRECT: %w", err)
	}
	r.meshRedirect = rule
	r.log.Infof("mesh gateway: *.dmsg / *.skynet → synthetic %s → proxy :%d (dials over mesh)", gw.PoolCIDR().String(), port)
	return nil
}

// teardown reverses setup, best-effort (logs but does not abort on errors).
func (r *Router) teardown() {
	// Remove the mesh-gateway REDIRECT and stop its proxy.
	if r.meshRedirect != nil {
		del := append([]string{"-t", "nat", "-D"}, r.meshRedirect[3:]...)
		if err := osutil.RunElevated("iptables", del...); err != nil {
			r.log.WithError(err).Warn("teardown: remove mesh gateway REDIRECT")
		}
	}
	if r.meshProxy != nil {
		_ = r.meshProxy.Close() //nolint:errcheck // best-effort close on shutdown
	}
	// Remove the TCPMSS clamp.
	if r.mssClamped {
		if err := osutil.RunElevated("iptables", r.mssClampArgs("-D")...); err != nil {
			r.log.WithError(err).Warn("teardown: remove TCPMSS clamp")
		}
	}
	// Remove the LAN→tun policy route+rule.
	if r.policyRouting {
		if err := netctl.DelRuleIif(r.cfg.LANInterface, lanPolicyTable); err != nil {
			r.log.WithError(err).Warn("teardown: remove policy rule")
		}
		if err := netctl.FlushTable(lanPolicyTable); err != nil {
			r.log.WithError(err).Warn("teardown: flush policy table")
		}
	}
	// Remove the FORWARD rules we added (-D mirrors each -A).
	for _, rule := range r.forwardRules {
		del := append([]string{"-D"}, rule[1:]...)
		if err := osutil.RunElevated("iptables", del...); err != nil {
			r.log.WithError(err).Warnf("teardown: remove FORWARD rule %s", strings.Join(rule, " "))
		}
	}
	if r.masquerading {
		if err := DisableIPMasquerading(r.tunIfc); err != nil {
			r.log.WithError(err).Warn("teardown: disable masquerading")
		}
	}
	if r.prevForwarding != "" && r.prevForwarding != "1" {
		if err := SetIPv4ForwardingValue(r.prevForwarding); err != nil {
			r.log.WithError(err).Warn("teardown: restore IPv4 forwarding")
		}
	}
	// ctx cancellation already signals the daemons; wait so they're reaped.
	for _, cmd := range r.daemons {
		_ = cmd.Wait() //nolint:errcheck // killed by ctx; Wait just reaps
	}
	// Drop the gateway address we assigned.
	if r.cfg.LANInterface != "" && r.cfg.Gateway != nil {
		cidr := fmt.Sprintf("%s/%d", r.cfg.Gateway, r.cfg.prefixLen())
		if err := netctl.AddrDel(r.cfg.LANInterface, cidr); err != nil {
			r.log.WithError(err).Warn("teardown: remove downstream address")
		}
	}
	if r.confDir != "" {
		_ = os.RemoveAll(r.confDir) //nolint:errcheck // temp conf dir, best-effort
	}
}

// prefixLen returns the subnet's CIDR prefix length (e.g. 24).
//
// Lives here rather than in router.go because it is only referenced from
// this linux-only file. In router.go (which builds on every platform) it
// is dead code on darwin/windows, where the `unused` linter fails the
// build — which it did, on every PR.
func (c RouterConfig) prefixLen() int {
	if c.Subnet == nil {
		return 0
	}
	ones, _ := c.Subnet.Mask.Size()
	return ones
}
