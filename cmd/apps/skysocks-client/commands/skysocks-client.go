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
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
	RootCmd.Flags().StringVar(&addr, "addr", visorconfig.SkysocksClientAddr, "Client address to listen on")
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
		fs.StringVar(&addr, "addr", visorconfig.SkysocksClientAddr, "Client address")
		fs.StringVar(&serverPK, "srv", "", "PubKey of server")
		fs.StringVar(&httpAddr, "http", "", "http proxy mode")
		fs.Int64Var(&tries, "tries", 3, "number of tries")
		fs.Int64Var(&retryDelay, "retry-time", 5, "delay between tries")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	r = netutil.NewRetrier(nil, time.Duration(retryDelay)*time.Second, netutil.DefaultMaxBackoff, tries, 1)

	appCl := app.NewClient(nil)
	defer appCl.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	if serverPK == "" {
		err := errors.New("Empty server PubKey. Exiting")
		print(fmt.Sprintf("%v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
		print(fmt.Sprintf("Invalid server PubKey: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	conn, err := dialServer(ctx, appCl, pk, serverPort)
	if err != nil {
		print(fmt.Sprintf("Failed to dial to a server: %v\n", err))
		setAppErr(appCl, err)
		return err
	}

	fmt.Printf("Connected to %v\n", pk)
	client, err := skysocks.NewClient(conn, appCl)
	if err != nil {
		print(fmt.Sprintf("Failed to create a new client: %v\n", err))
		setAppErr(appCl, err)
		return err
	}
	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(visorconfig.SkysocksClientName, nil)
		if err != nil {
			setAppErr(appCl, err)
			print(fmt.Sprintf("Error creating ipc server for skysocks: %v\n", err))
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
					print(fmt.Sprintf("%v\n", err))
				}
			case <-ctx.Done():
				if err := client.Close(); err != nil {
					print(fmt.Sprintf("%v\n", err))
				}
			}
		}()
	}

	fmt.Printf("Serving proxy client %v\n", addr)
	setAppStatus(appCl, appserver.AppDetailedStatusRunning)
	httpCtx, httpCancel := context.WithCancel(ctx)
	defer httpCancel()
	if httpAddr != "" {
		go httpProxy(httpCtx, httpAddr, addr)
	}
	if err := client.ListenAndServe(addr); err != nil {
		print(fmt.Sprintf("Error serving proxy client: %v\n", err))
		return err
	}
	setAppStatus(appCl, appserver.AppDetailedStatusStopped)
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

func setAppErr(appCl *app.Client, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", err, appErr))
	}
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func httpProxy(ctx context.Context, httpAddr, sockscAddr string) {
	proxy := goproxy.NewProxyHttpServer()

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1%s", sockscAddr))
	if err != nil {
		print(fmt.Sprintf("Failed to parse socks address: %v\n", err))
		return
	}

	proxy.Tr.Proxy = http.ProxyURL(proxyURL)

	fmt.Printf("Serving http proxy %v\n", httpAddr)
	httpProxySrv := &http.Server{
		Addr:              httpAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		//nolint:errcheck
		httpProxySrv.Close() //nolint:errcheck,gosec
		print("Stopping http proxy")
	}()

	if err := httpProxySrv.ListenAndServe(); err != nil {
		print(fmt.Sprintf("Error serving http proxy: %v\n", err))
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func setAppPort(appCl *app.Client, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		print(fmt.Sprintf("Failed to set port %v: %v\n", port, err))
	}
}
