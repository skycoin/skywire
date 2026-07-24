// Package commands cmd/apps/vpn-router/commands/vpn-router.go c4-app-vpn
package commands

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/vpn"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

var (
	lanIfc          string
	tunIfc          string
	subnetCIDR      string
	dhcpStart       string
	dhcpEnd         string
	dnsAddr         string
	leaseTime       string
	wifi            bool
	ssid            string
	passphrase      string
	band            string
	channel         int
	country         string
	openWiFi        bool
	meshGateway     bool
	meshGatewayOnly bool
	meshGWCIDR      string
	meshBind        string
	meshDNSPort     int
	meshTLS         bool
	meshCACert      string
	meshCAKey       string
	meshAliases     []string
	appPort         uint16
)

// defaultMeshCADir is where the mesh gateway persists its self-generated CA so
// LAN clients only need to trust it once (survives restarts).
var defaultMeshCADir = filepath.Join(skyenv.PackageConfig().LocalPath, "mesh-gateway-ca")

// registerFlags declares the router flags on a pflag.FlagSet. Both the cobra
// RootCmd and the internal-launcher parse path use it, so a flag is never
// present in one and missing in the other.
func registerFlags(fs *pflag.FlagSet) {
	fs.StringVar(&lanIfc, "lan-ifc", "", "downstream interface serving clients (e.g. eth1 or wlan0) — REQUIRED")
	fs.StringVar(&tunIfc, "tun-ifc", "", "upstream mesh-VPN tunnel to NAT into (empty = auto-detect the first tun* the vpn-client brings up)")
	fs.StringVar(&subnetCIDR, "subnet", "192.168.42.1/24", "router gateway address + downstream subnet, as <gateway-ip>/<prefix>")
	fs.StringVar(&dhcpStart, "dhcp-start", "", "DHCP pool start (empty = .10 of the subnet)")
	fs.StringVar(&dhcpEnd, "dhcp-end", "", "DHCP pool end (empty = .254 of the subnet)")
	fs.StringVar(&dnsAddr, "dns", "", "DNS advertised to clients over DHCP (empty = the router itself)")
	fs.StringVar(&leaseTime, "lease", "12h", "DHCP lease time")
	fs.BoolVar(&wifi, "wifi", false, "WiFi-out: also run hostapd to beacon an AP on the downstream interface")
	fs.StringVar(&ssid, "ssid", "", "WiFi SSID (with --wifi)")
	fs.StringVar(&passphrase, "passphrase", "", "WiFi WPA2 passphrase, 8–63 chars (with --wifi)")
	fs.StringVar(&band, "band", "2.4", "WiFi band: 2.4 or 5 (with --wifi)")
	fs.IntVar(&channel, "channel", 0, "WiFi channel (0 = default for the band)")
	fs.StringVar(&country, "country", "US", "WiFi regulatory country code (with --wifi)")
	fs.BoolVar(&openWiFi, "open", false, "allow an open (passphrase-less) WiFi network")
	fs.BoolVar(&meshGateway, "mesh-gateway", false, "mesh gateway: resolve *.dmsg / *.skynet for downstream clients and proxy them over the mesh (visor-launched only)")
	fs.BoolVar(&meshGatewayOnly, "mesh-gateway-only", false, "standalone mesh gateway: ONLY serve *.dmsg / *.skynet DNS + transparent proxy for the LAN (no DHCP / NAT / tunnel) — for a board behind an existing OpenWRT/DD-WRT router (visor-launched only)")
	fs.StringVar(&meshGWCIDR, "mesh-gateway-cidr", "", "synthetic-IP pool the mesh gateway leases from (empty = 100.64.0.0/16)")
	fs.StringVar(&meshBind, "mesh-bind", "", "standalone mesh gateway: address the DNS + transparent proxy bind (empty = 0.0.0.0)")
	fs.IntVar(&meshDNSPort, "mesh-dns-port", 53, "standalone mesh gateway: DNS port the upstream router forwards .dmsg/.skynet to")
	fs.BoolVar(&meshTLS, "mesh-gateway-tls", false, "mesh gateway: TLS-MITM HTTPS to *.dmsg/*.skynet with a self-generated CA (LAN clients must trust it)")
	fs.StringVar(&meshCACert, "mesh-gateway-ca", "", "mesh-gateway CA cert path (empty = <local>/mesh-gateway-ca/ca.pem; generated if absent)")
	fs.StringVar(&meshCAKey, "mesh-gateway-ca-key", "", "mesh-gateway CA key path (empty = <local>/mesh-gateway-ca/ca.key)")
	fs.StringArrayVar(&meshAliases, "mesh-alias", nil, "mesh-gateway friendly name → PK, as name=<pk> (repeatable); reach http://<name>.dmsg")
	fs.Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

func init() {
	launcher.RegisterApp(skyenv.VPNRouterName, RunVPNRouter)
	registerFlags(RootCmd.Flags())
}

// RootCmd is the vpn-router app command.
var RootCmd = &cobra.Command{
	Use:                   "vpn-router",
	Short:                 "skywire vpn router (gateway / WiFi-AP) application",
	Long:                  calvin.AsciiFont("vpn-router"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	RunE: func(_ *cobra.Command, _ []string) error {
		return RunVPNRouter(context.Background(), nil)
	},
}

// Execute executes the root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// RunVPNRouter is the launcher AppFunc: it brings up the downstream interface,
// DHCP/DNS (and optionally a WiFi AP), and NATs client traffic into the mesh-VPN
// tunnel maintained by the companion vpn-client app.
func RunVPNRouter(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet("vpn-router", pflag.ContinueOnError)
		registerFlags(fs)
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	// Standalone mode: when the app is not launched by a visor (no
	// PROC_CONFIG in the environment), run the router as a plain daemon
	// rather than fatally requiring the visor's app-server. The router's
	// job — serve DHCP/DNS on the downstream interface and NAT into a tun
	// the vpn-client (or any tunnel) provides — needs no visor coupling,
	// so this is a first-class way to run it: on a host OS, for testing,
	// or wherever the mesh tunnel is managed separately. app-server status
	// reporting is simply skipped (there's nothing to report to).
	if _, launched := os.LookupEnv(appcommon.EnvProcConfig); !launched {
		return runStandalone(ctx)
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	logger := appCl.Log()

	if appPort != 0 {
		if err := appCl.SetAppPort(routing.Port(appPort)); err != nil {
			logger.WithError(err).WithField("port", appPort).Warn("Failed to set app port")
		}
	}

	bi := buildinfo.Get()
	logger.Infof("Version %q built on %q against commit %q", bi.Version, bi.Date, bi.Commit)

	// Standalone mesh-gateway mode: serve .dmsg/.skynet DNS + transparent proxy
	// for the LAN only, with no DHCP/NAT/tunnel. Runs behind an existing router.
	if meshGatewayOnly {
		setAppStatus(appCl, logger, appserver.AppDetailedStatusRunning)
		defer setAppStatus(appCl, logger, appserver.AppDetailedStatusStopped)
		if err := runMeshGatewayOnly(ctx, vpn.MeshDialer(appCl), logger); err != nil {
			logger.WithError(err).Error("standalone mesh gateway exited with error")
			setAppErr(appCl, logger, err)
			return err
		}
		return nil
	}

	cfg, err := buildRouterConfig(vpn.MeshDialer(appCl))
	if err != nil {
		logger.WithError(err).Error("invalid vpn-router configuration")
		setAppErr(appCl, logger, err)
		return err
	}

	router, err := vpn.NewRouter(cfg, logger)
	if err != nil {
		logger.WithError(err).Error("failed to create vpn-router")
		setAppErr(appCl, logger, err)
		return err
	}

	setAppStatus(appCl, logger, appserver.AppDetailedStatusRunning)
	defer setAppStatus(appCl, logger, appserver.AppDetailedStatusStopped)

	if err := router.Run(ctx); err != nil {
		logger.WithError(err).Error("vpn-router exited with error")
		setAppErr(appCl, logger, err)
		return err
	}
	return nil
}

// runStandalone runs the router without any visor app-server coupling: build
// config from flags, construct the Router, and Run until a signal (or ctx)
// cancels. Used when the binary is invoked directly (no PROC_CONFIG), e.g. for
// on-host operation or hardware testing.
func runStandalone(ctx context.Context) error {
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	// The mesh gateway (full or standalone) dials the mesh through the visor's
	// app-server, which a directly-invoked binary doesn't have. Fail clearly
	// rather than silently degrading to a plain (no-mesh) router.
	if meshGatewayOnly || meshGateway {
		return fmt.Errorf("--mesh-gateway / --mesh-gateway-only need the visor app-server to dial the mesh; launch vpn-router from a visor, not directly")
	}

	// Standalone has no visor app-server, so no mesh dialer (nil). buildRouterConfig
	// rejects --mesh-gateway here with a clear error.
	cfg, err := buildRouterConfig(nil)
	if err != nil {
		return fmt.Errorf("invalid vpn-router configuration: %w", err)
	}
	router, err := vpn.NewRouter(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create vpn-router: %w", err)
	}

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("vpn-router running standalone (no visor app-server; status reporting disabled)")
	return router.Run(sigCtx)
}

// buildRouterConfig turns the parsed flags into a vpn.RouterConfig. dialer backs
// the mesh gateway when --mesh-gateway is set; it is nil in standalone mode (no
// visor app-server), where the mesh gateway cannot run.
func buildRouterConfig(dialer meshgw.MeshDial) (vpn.RouterConfig, error) {
	gw, subnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return vpn.RouterConfig{}, fmt.Errorf("invalid --subnet %q (want <gateway-ip>/<prefix>, e.g. 192.168.42.1/24): %w", subnetCIDR, err)
	}
	cfg := vpn.RouterConfig{
		LANInterface: lanIfc,
		TUNInterface: tunIfc,
		Gateway:      gw,
		Subnet:       subnet,
		LeaseTime:    leaseTime,
	}
	if dhcpStart != "" {
		if cfg.DHCPStart = net.ParseIP(dhcpStart); cfg.DHCPStart == nil {
			return cfg, fmt.Errorf("invalid --dhcp-start %q", dhcpStart)
		}
	}
	if dhcpEnd != "" {
		if cfg.DHCPEnd = net.ParseIP(dhcpEnd); cfg.DHCPEnd == nil {
			return cfg, fmt.Errorf("invalid --dhcp-end %q", dhcpEnd)
		}
	}
	if dnsAddr != "" {
		if cfg.DNS = net.ParseIP(dnsAddr); cfg.DNS == nil {
			return cfg, fmt.Errorf("invalid --dns %q", dnsAddr)
		}
	}
	if wifi {
		cfg.WiFi = &vpn.WiFiConfig{
			SSID:        ssid,
			Passphrase:  passphrase,
			Band:        band,
			Channel:     channel,
			CountryCode: country,
			AllowOpen:   openWiFi,
		}
	}
	if meshGateway {
		if dialer == nil {
			return cfg, fmt.Errorf("--mesh-gateway needs the visor app-server to dial the mesh; it cannot run in standalone mode")
		}
		cfg.MeshDial = dialer
		cfg.MeshGatewayCIDR = meshGWCIDR
		if len(meshAliases) > 0 {
			aliases, err := vpn.ParseMeshAliases(meshAliases)
			if err != nil {
				return cfg, err
			}
			cfg.MeshAliases = aliases
		}
		if meshTLS {
			minter, err := vpn.LoadOrCreateMeshCA(meshCACert, meshCAKey, defaultMeshCADir)
			if err != nil {
				return cfg, err
			}
			cfg.MeshTLSMinter = minter
		}
	}
	return cfg, nil
}

func setAppErr(appCl *app.Client, logg logrus.FieldLogger, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		logg.WithError(appErr).WithField("original_error", err).Warn("Failed to set error")
	}
}

func setAppStatus(appCl *app.Client, logg logrus.FieldLogger, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		logg.WithError(err).WithField("status", status).Warn("Failed to set status")
	}
}
