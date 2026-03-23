// Package commands cmd/apps/skysocks-client/skysocks-client.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/elazarl/goproxy"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skysocks"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
)

const (
	netType    = appnet.TypeSkynet
	serverPort = routing.Port(3) // skysocks server port
)

var (
	r          *netutil.Retrier
	addr       string
	serverPK   string
	httpAddr   string
	retryDelay int64
	tries      int64
	appPort    uint16
)

func init() {
	launcher.RegisterApp("skysocks-client", RunSkysocksClient)
	RootCmd.Flags().StringVar(&addr, "addr", skyenv.SkysocksClientAddr, "Client address to listen on")
	RootCmd.Flags().StringVar(&serverPK, "srv", "", "PubKey of the server to connect to")
	RootCmd.Flags().StringVar(&httpAddr, "http", "", "http proxy mode")
	RootCmd.Flags().Int64Var(&tries, "tries", 3, "number of tries")
	RootCmd.Flags().Int64Var(&retryDelay, "retry-time", 5, "delay between each try")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
}

// RootCmd is the root command for skysocks
var RootCmd = &cobra.Command{
	Use:                   "skysocks-client",
	Short:                 "skywire socks5 proxy client application",
	Long:                  calvin.AsciiFont("skysocks-client"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkysocksClient(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkysocksClient runs the skysocks client app logic.
func RunSkysocksClient(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skysocks-client", pflag.ContinueOnError)
		fs.StringVar(&addr, "addr", skyenv.SkysocksClientAddr, "Client address")
		fs.StringVar(&serverPK, "srv", "", "PubKey of server")
		fs.StringVar(&httpAddr, "http", "", "http proxy mode")
		fs.Int64Var(&tries, "tries", 3, "number of tries")
		fs.Int64Var(&retryDelay, "retry-time", 5, "delay between tries")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	log := appCl.Log()

	r = netutil.NewRetrier(log, time.Duration(retryDelay)*time.Second, netutil.DefaultMaxBackoff, tries, 1)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
	}
	setAppPort(appCl, log, port)

	bi := buildinfo.Get()
	log.Infof("Version %q built on %q against commit %q", bi.Version, bi.Date, bi.Commit)

	if serverPK == "" {
		err := errors.New("Empty server PubKey. Exiting")
		log.Error(err)
		setAppErr(appCl, log, err)
		return err
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
		log.WithError(err).Error("Invalid server PubKey")
		setAppErr(appCl, log, err)
		return err
	}

	defer setAppStatus(appCl, log, appserver.AppDetailedStatusStopped)

	conn, err := dialServer(ctx, appCl, pk, serverPort)
	if err != nil {
		log.WithError(err).Error("Failed to dial to a server")
		setAppErr(appCl, log, err)
		return err
	}

	log.Infof("Connected to %v", pk)
	client, err := skysocks.NewClient(conn, appCl)
	if err != nil {
		log.WithError(err).Error("Failed to create a new client")
		setAppErr(appCl, log, err)
		return err
	}
	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(skyenv.SkysocksClientName, nil)
		if err != nil {
			setAppErr(appCl, log, err)
			log.WithError(err).Error("Error creating ipc server for skysocks")
			return err
		}
		go client.ListenIPC(ipcClient)
	} else {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)
		go func() {
			select {
			case <-termCh:
				if err := client.Close(); err != nil {
					log.WithError(err).Warn("Error closing client on interrupt")
				}
			case <-ctx.Done():
				if err := client.Close(); err != nil {
					log.WithError(err).Warn("Error closing client on context done")
				}
			}
		}()
	}

	log.Infof("Serving proxy client %v", addr)
	setAppStatus(appCl, log, appserver.AppDetailedStatusRunning)
	httpCtx, httpCancel := context.WithCancel(ctx)
	defer httpCancel()
	if httpAddr != "" {
		go httpProxy(httpCtx, httpAddr, addr, log)
	}
	//nolint:staticcheck // ListenAndServe blocks until error; check is necessary
	if err := client.ListenAndServe(addr); err != nil {
		log.WithError(err).Error("Error serving proxy client")
		return err
	}
	setAppStatus(appCl, log, appserver.AppDetailedStatusStopped)
	return nil
}

func dialServer(ctx context.Context, appCl *app.Client, pk cipher.PubKey, port routing.Port) (net.Conn, error) {
	//nolint:errcheck
	appCl.SetDetailedStatus(appserver.AppDetailedStatusStarting) //nolint:errcheck,gosec
	var conn net.Conn
	err := r.Do(ctx, func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   port,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
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

func httpProxy(ctx context.Context, httpAddr, sockscAddr string, log logrus.FieldLogger) {
	proxy := goproxy.NewProxyHttpServer()

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1%s", sockscAddr))
	if err != nil {
		log.WithError(err).Error("Failed to parse socks address")
		return
	}

	proxy.Tr.Proxy = http.ProxyURL(proxyURL)

	log.Infof("Serving http proxy %v", httpAddr)
	httpProxySrv := &http.Server{
		Addr:              httpAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		//nolint:errcheck
		httpProxySrv.Close() //nolint:errcheck,gosec
		log.Info("Stopping http proxy")
	}()

	if err := httpProxySrv.ListenAndServe(); err != nil {
		log.WithError(err).Error("Error serving http proxy")
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
