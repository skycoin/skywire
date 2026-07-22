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
	"github.com/skycoin/skywire/pkg/vpnrouter"
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

	// state captured for restoration on teardown.
	prevForwarding string
	masquerading   bool
	forwardRules   [][]string // iptables argv sets we added to the FORWARD chain
	policyRouting  bool       // true once the LAN→tun policy route+rule are installed
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

	// 3. DHCP + DNS for the downstream subnet — an embedded, pure-Go engine
	// (vendored router7 dhcp4d + dns), replacing the external dnsmasq the app
	// used to shell out to. StartLAN blocks until ctx is cancelled, so run it
	// in the background; a startup error surfaces via the log.
	go func() {
		if err := vpnrouter.StartLAN(ctx, r.cfg.LANInterface, r.confDir); err != nil && ctx.Err() == nil {
			r.log.WithError(err).Error("embedded DHCP/DNS server exited")
		}
	}()

	// 4. Wait for the vpn-client tunnel, then forward + NAT into it.
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
	_ = osutil.RunElevated("ip", "addr", "flush", "dev", lan) //nolint:errcheck // best-effort clean slate
	if err := osutil.RunElevated("ip", "addr", "add", cidr, "dev", lan); err != nil {
		return fmt.Errorf("assign %s to %s: %w", cidr, lan, err)
	}
	if err := osutil.RunElevated("ip", "link", "set", lan, "up"); err != nil {
		return fmt.Errorf("bring up %s: %w", lan, err)
	}
	r.log.Infof("downstream %s = %s", lan, cidr)
	return nil
}

func (r *Router) startHostapd(ctx context.Context) error {
	if _, err := exec.LookPath("hostapd"); err != nil {
		return fmt.Errorf("hostapd not found — install it for the WiFi-out variant: %w", err)
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
// default route out the tun and an `ip rule` matching only the LAN subnet as
// source, so forwarded client packets egress via the VPN while everything the
// router originates (dmsg/skywire included) keeps the main table.
func (r *Router) setupPolicyRouting() error {
	table := strconv.Itoa(lanPolicyTable)
	subnet := r.cfg.Subnet.String()

	// default route via the tun device in our private table.
	if err := osutil.RunElevated("ip", "route", "replace", "default", "dev", r.tunIfc, "table", table); err != nil {
		return fmt.Errorf("policy route (default dev %s table %s): %w", r.tunIfc, table, err)
	}
	// match LAN-sourced traffic into that table. Delete any stale copy first so
	// a restart doesn't stack duplicate rules.
	_ = osutil.RunElevated("ip", "rule", "del", "from", subnet, "lookup", table) //nolint:errcheck // best-effort cleanup of a stale rule
	if err := osutil.RunElevated("ip", "rule", "add", "from", subnet, "lookup", table); err != nil {
		return fmt.Errorf("policy rule (from %s lookup %s): %w", subnet, table, err)
	}
	r.policyRouting = true
	r.log.Infof("policy routing: %s → %s (table %s); router uplink unchanged", subnet, r.tunIfc, table)
	return nil
}

// teardown reverses setup, best-effort (logs but does not abort on errors).
func (r *Router) teardown() {
	// Remove the LAN→tun policy route+rule.
	if r.policyRouting {
		table := strconv.Itoa(lanPolicyTable)
		subnet := r.cfg.Subnet.String()
		if err := osutil.RunElevated("ip", "rule", "del", "from", subnet, "lookup", table); err != nil {
			r.log.WithError(err).Warn("teardown: remove policy rule")
		}
		if err := osutil.RunElevated("ip", "route", "flush", "table", table); err != nil {
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
		if err := osutil.RunElevated("ip", "addr", "del", cidr, "dev", r.cfg.LANInterface); err != nil {
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
