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
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skysocks"
)

const (
	netType    = appnet.TypeSkynet
	serverPort = routing.Port(3) // skysocks server port
)

var (
	r              *netutil.Retrier
	addr           string
	serverPK       string
	httpAddr       string
	retryDelay     int64
	tries          int64
	appPort        uint16
	reconnect      bool
	reconnectDelay int64
)

func init() {
	launcher.RegisterApp("skysocks-client", RunSkysocksClient)
	RootCmd.Flags().StringVar(&addr, "addr", skyenv.SkysocksClientAddr, "Client address to listen on")
	RootCmd.Flags().StringVar(&serverPK, "srv", "", "PubKey of the server to connect to")
	RootCmd.Flags().StringVar(&httpAddr, "http", "", "http proxy mode")
	RootCmd.Flags().Int64Var(&tries, "tries", 3, "number of tries")
	RootCmd.Flags().Int64Var(&retryDelay, "retry-time", 5, "delay between each try")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	// In-proc reconnect loop. When set, the proc does not exit
	// on dial / stream failure — it sleeps --reconnect-delay
	// seconds and re-dials. Closes the ~3-5s gap that the
	// restart_policy:always cycle leaves during remote-visor
	// restarts (e.g., upstream auto-update). The visor-side
	// restart_policy is still useful as a backstop for unrecov-
	// erable proc-level failures.
	RootCmd.Flags().BoolVar(&reconnect, "reconnect", false, "in-process reconnect on stream failure (vs exiting)")
	RootCmd.Flags().Int64Var(&reconnectDelay, "reconnect-delay", 2, "seconds between in-process reconnect attempts")
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
		fs.BoolVar(&reconnect, "reconnect", false, "in-process reconnect on stream failure")
		fs.Int64Var(&reconnectDelay, "reconnect-delay", 2, "seconds between reconnect attempts")
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

	// httpProxy is bound once per proc lifetime — its connection
	// per request to the local SOCKS5 listener means it survives
	// reconnect cycles transparently (browser sees a brief
	// connection-refused on the gap, retries succeed).
	httpCtx, httpCancel := context.WithCancel(ctx)
	defer httpCancel()
	if httpAddr != "" {
		go httpProxy(httpCtx, httpAddr, addr, log)
	}

	// SIGINT goroutine cancels the outer ctx so the cycle loop
	// breaks out cleanly. Installed once for the proc lifetime;
	// each cycle's client.ListenAndServe returns when the
	// underlying conn closes (we close it in the cycle's deferred
	// cleanup when ctx fires).
	cycleCtx, cycleCancel := context.WithCancel(ctx)
	defer cycleCancel()
	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)
		go func() {
			select {
			case <-termCh:
				cycleCancel()
			case <-cycleCtx.Done():
			}
		}()
	}

	// runCycle does one connect + serve attempt. Returns nil
	// only on a clean ctx-done stop; any other return is a
	// (re-)connect trigger when --reconnect is set.
	runCycle := func() error {
		conn, err := dialServer(cycleCtx, appCl, pk, serverPort)
		if err != nil {
			return fmt.Errorf("dial server: %w", err)
		}
		log.Infof("Connected to %v", pk)
		client, err := skysocks.NewClient(conn, appCl)
		if err != nil {
			return fmt.Errorf("new client: %w", err)
		}
		// Close the client when the outer ctx fires so
		// ListenAndServe returns. With --reconnect the loop
		// just iterates; without it the loop body returns and
		// the proc exits (existing behavior).
		closeOnCtx := make(chan struct{})
		go func() {
			select {
			case <-cycleCtx.Done():
				if cerr := client.Close(); cerr != nil {
					log.WithError(cerr).Warn("Error closing client on context done")
				}
			case <-closeOnCtx:
			}
		}()
		defer close(closeOnCtx)

		if runtime.GOOS == "windows" {
			ipcClient, err := ipc.StartClient(skyenv.SkysocksClientName, nil)
			if err != nil {
				return fmt.Errorf("start IPC client: %w", err)
			}
			go client.ListenIPC(ipcClient)
		}

		log.Infof("Serving proxy client %v", addr)
		setAppStatus(appCl, log, appserver.AppDetailedStatusRunning)
		//nolint:staticcheck
		if err := client.ListenAndServe(addr); err != nil {
			return fmt.Errorf("serve proxy client: %w", err)
		}
		return nil
	}

	if !reconnect {
		if err := runCycle(); err != nil {
			log.WithError(err).Error("skysocks-client failed")
			setAppErr(appCl, log, err)
			return err
		}
		setAppStatus(appCl, log, appserver.AppDetailedStatusStopped)
		return nil
	}

	// Reconnect mode: loop until cycleCtx is canceled (signal /
	// parent visor stop). Each iteration is one connect+serve
	// cycle; a failure logs + sleeps + retries indefinitely.
	// The visor-side restart_policy still wins as a backstop —
	// if this proc itself panics, the restart loop catches it.
	delay := time.Duration(reconnectDelay) * time.Second
	for {
		if err := runCycle(); err != nil {
			if cycleCtx.Err() != nil {
				// Ctx-induced unwind, not a real failure.
				return nil
			}
			log.WithError(err).Warnf("Reconnecting in %v", delay)
			setAppStatus(appCl, log, appserver.AppDetailedStatusReconnecting)
		}
		if cycleCtx.Err() != nil {
			return nil
		}
		select {
		case <-cycleCtx.Done():
			return nil
		case <-time.After(delay):
		}
	}
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
