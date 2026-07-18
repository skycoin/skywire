// Package commands cmd/apps/vpn-router/commands/vpn-router.go c4-app-vpn
package commands

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/vpn"
)

var (
	lanIfc     string
	tunIfc     string
	subnetCIDR string
	dhcpStart  string
	dhcpEnd    string
	dnsAddr    string
	leaseTime  string
	wifi       bool
	ssid       string
	passphrase string
	band       string
	channel    int
	country    string
	openWiFi   bool
	appPort    uint16
)

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

	cfg, err := buildRouterConfig()
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

// buildRouterConfig turns the parsed flags into a vpn.RouterConfig.
func buildRouterConfig() (vpn.RouterConfig, error) {
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
