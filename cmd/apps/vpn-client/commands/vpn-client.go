// Package commands vpn-client.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	serverPKStr string
	localPKStr  string
	localSKStr  string
	killswitch  bool
	dnsAddr     string
	appPort     uint16
)

func init() {
	launcher.RegisterApp("vpn-client", RunVPNClient)
	RootCmd.Flags().StringVar(&serverPKStr, "srv", "", "PubKey of the server to connect to")
	RootCmd.Flags().StringVar(&localPKStr, "pk", "", "local pubkey")
	RootCmd.Flags().StringVar(&localSKStr, "sk", "", "local seckey")
	RootCmd.Flags().BoolVar(&killswitch, "killswitch", false, "If set, the Internet won't be restored during reconnection attempts")
	RootCmd.Flags().StringVar(&dnsAddr, "dns", "", "address of DNS want set to tun")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use:                   "vpn-client",
	Short:                 "skywire vpn client application",
	Long:                  calvin.AsciiFont("vpn-client"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunVPNClient(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunVPNClient runs the VPN client app logic.
func RunVPNClient(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("vpn-client", pflag.ContinueOnError)
		fs.StringVar(&serverPKStr, "srv", "", "PubKey of server")
		fs.StringVar(&localPKStr, "pk", "", "local pubkey")
		fs.StringVar(&localSKStr, "sk", "", "local seckey")
		fs.BoolVar(&killswitch, "killswitch", false, "killswitch")
		fs.StringVar(&dnsAddr, "dns", "", "DNS address")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	var directIPsCh, nonDirectIPsCh = make(chan net.IP, 100), make(chan net.IP, 100)
	// done channel signals event callbacks to stop sending to IP channels
	done := make(chan struct{})
	defer close(done)
	defer close(directIPsCh)
	defer close(nonDirectIPsCh)

	eventSub := appevent.NewSubscriber()

	// Create app client early to get logger
	appCl := app.NewClient(eventSub)
	defer appCl.Close()
	logger := appCl.Log()

	parseIP := func(addr string) net.IP {
		ip, ok, err := vpn.ParseIP(addr)
		if err != nil {
			logger.WithError(err).WithField("addr", addr).Warn("Failed to parse IP")
			return nil
		}
		if !ok {
			logger.WithField("addr", addr).Warn("Failed to parse IP")
			return nil
		}

		return ip
	}

	eventSub.OnTCPDial(func(data appevent.TCPDialData) {
		if ip := parseIP(data.RemoteAddr); ip != nil {
			select {
			case directIPsCh <- ip:
			case <-done:
			}
		}
	})

	eventSub.OnTCPClose(func(data appevent.TCPCloseData) {
		if ip := parseIP(data.RemoteAddr); ip != nil {
			select {
			case nonDirectIPsCh <- ip:
			case <-done:
			}
		}
	})

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
	}
	setAppPort(appCl, logger, port)

	bi := buildinfo.Get()
	logger.Infof("Version %q built on %q against commit %q", bi.Version, bi.Date, bi.Commit)

	if serverPKStr == "" {
		err := errors.New("VPN server pub key is missing")
		logger.Error(err)
		setAppErr(appCl, logger, err)
		return err
	}

	serverPK := cipher.PubKey{}
	if err := serverPK.UnmarshalText([]byte(serverPKStr)); err != nil {
		logger.WithError(err).Error("Invalid VPN server pub key")
		setAppErr(appCl, logger, err)
		return err
	}

	localPK := cipher.PubKey{}
	if localPKStr != "" {
		if err := localPK.UnmarshalText([]byte(localPKStr)); err != nil {
			logger.WithError(err).Error("Invalid local PK")
			setAppErr(appCl, logger, err)
			return err
		}
	}

	localSK := cipher.SecKey{}
	if localSKStr != "" {
		if err := localSK.UnmarshalText([]byte(localSKStr)); err != nil {
			logger.WithError(err).Error("Invalid local SK")
			setAppErr(appCl, logger, err)
			return err
		}
	}

	var dnsAddress string
	if dnsAddr != "" {
		dnsIP := parseIP(dnsAddr)
		if dnsIP == nil {
			logger.Warn("Invalid DNS Address value. VPN will use current machine DNS.")
			dnsAddress = ""
		} else {
			dnsAddress = dnsIP.String()
		}
	}

	logger.Infof("Connecting to VPN server %s", serverPK.String())

	vpnClientCfg := vpn.ClientConfig{
		Killswitch: killswitch,
		ServerPK:   serverPK,
		DNSAddr:    dnsAddress,
	}

	vpnClient, err := vpn.NewClient(vpnClientCfg, appCl)
	if err != nil {
		logger.WithError(err).Error("Error creating VPN client")
		setAppErr(appCl, logger, err)
		return err
	}

	var directRoutesDone bool
	for !directRoutesDone {
		select {
		case ip := <-directIPsCh:
			if err := vpnClient.AddDirectRoute(ip); err != nil {
				logger.WithError(err).WithField("ip", ip.String()).Warn("Failed to setup direct route")
				setAppErr(appCl, logger, err)
			}
		default:
			directRoutesDone = true
		}
	}

	go func() {
		for ip := range directIPsCh {
			if err := vpnClient.AddDirectRoute(ip); err != nil {
				logger.WithError(err).WithField("ip", ip.String()).Warn("Failed to setup direct route")
				setAppErr(appCl, logger, err)
			}
		}
	}()

	go func() {
		for ip := range nonDirectIPsCh {
			if err := vpnClient.RemoveDirectRoute(ip); err != nil {
				logger.WithError(err).WithField("ip", ip.String()).Warn("Failed to remove direct route")
				setAppErr(appCl, logger, err)
			}
		}
	}()

	if runtime.GOOS != "windows" {
		osSigs := make(chan os.Signal, 2)
		sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
		for _, sig := range sigs {
			signal.Notify(osSigs, sig)
		}

		go func() {
			<-osSigs
			vpnClient.Close()
		}()
	} else {
		ipcClient, err := ipc.StartClient(visorconfig.VPNClientName, nil)
		if err != nil {
			logger.WithError(err).Error("Error creating ipc server for VPN client")
			setAppErr(appCl, logger, err)
			return err
		}
		go vpnClient.ListenIPC(ipcClient)
	}

	defer setAppStatus(appCl, logger, appserver.AppDetailedStatusStopped)

	serveCh := make(chan error, 1)
	go func() {
		serveCh <- vpnClient.Serve()
	}()

	select {
	case err := <-serveCh:
		if err != nil {
			logger.WithError(err).Error("Failed to serve VPN")
			return err
		}
	case <-ctx.Done():
		vpnClient.Close()
		return nil
	}

	return nil
}

func setAppErr(appCl *app.Client, log logrus.FieldLogger, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		log.WithError(appErr).WithField("original_error", err).Warn("Failed to set error")
	}
}

func setAppStatus(appCl *app.Client, log logrus.FieldLogger, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		log.WithError(err).WithField("status", status).Warn("Failed to set status")
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func setAppPort(appCl *app.Client, log logrus.FieldLogger, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		log.WithError(err).WithField("port", port).Warn("Failed to set port")
	}
}
