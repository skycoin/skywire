// /* cmd/apps/skysocks-client/skysocks-client.go
/*
proxy client app for skywire visor
*/
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/elazarl/goproxy"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	netType   = appnet.TypeSkynet
	socksPort = routing.Port(3)
)

var r = netutil.NewRetrier(nil, time.Second, netutil.DefaultMaxBackoff, 0, 1)

func dialServer(ctx context.Context, appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	appCl.SetDetailedStatus(appserver.AppDetailedStatusStarting) //nolint
	var conn net.Conn
	err := r.Do(ctx, func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   socksPort,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func main() {
	appCl := app.NewClient(nil)
	defer appCl.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	var addr = flag.String("addr", visorconfig.SkysocksClientAddr, "Client address to listen on")
	var serverPK = flag.String("srv", "", "PubKey of the server to connect to")
	var httpAddr = flag.String("http", "", "Address for http-proxy")
	flag.Parse()

	if *serverPK == "" {
		err := errors.New("Empty server PubKey. Exiting")
		print(fmt.Sprintf("%v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(*serverPK)); err != nil {
		print(fmt.Sprintf("Invalid server PubKey: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}
	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)
	setAppPort(appCl, appCl.Config().RoutingPort)
	for {
		conn, err := dialServer(ctx, appCl, pk)
		if err != nil {
			print(fmt.Sprintf("Failed to dial to a server: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}

		fmt.Printf("Connected to %v\n", pk)
		client, err := skysocks.NewClient(conn, appCl)
		if err != nil {
			print(fmt.Sprintf("Failed to create a new client: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}

		fmt.Printf("Serving proxy client %v\n", *addr)
		setAppStatus(appCl, appserver.AppDetailedStatusRunning)
		httpCtx, httpCancel := context.WithCancel(ctx)
		if *httpAddr != "" {
			go httpProxy(httpCtx, httpAddr, addr)
		}
		if err := client.ListenAndServe(*addr); err != nil {
			print(fmt.Sprintf("Error serving proxy client: %v\n", err))
		}
		httpCancel()
		// need to filter this out, cause usually client failure means app conn is already closed
		if err := conn.Close(); err != nil && err != io.ErrClosedPipe {
			print(fmt.Sprintf("Error closing app conn: %v\n", err))
		}

		fmt.Println("Reconnecting to skysocks server")
		setAppStatus(appCl, appserver.AppDetailedStatusReconnecting)
	}
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

func setAppPort(appCl *app.Client, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		print(fmt.Sprintf("Failed to set port %v: %v\n", port, err))
	}
}

func httpProxy(ctx context.Context, httpAddr, sockscAddr *string) {
	proxy := goproxy.NewProxyHttpServer()

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1%s", *sockscAddr)) //nolint
	if err != nil {
		print(fmt.Sprintf("Failed to parse socks address: %v\n", err))
		return
	}

	proxy.Tr.Proxy = http.ProxyURL(proxyURL)

	fmt.Printf("Serving http proxy %v\n", *httpAddr)
	httpProxySrv := &http.Server{Addr: *httpAddr, Handler: proxy} //nolint

	go func() {
		<-ctx.Done()
		httpProxySrv.Close() //nolint
		print("Stopping http proxy")
	}()

	if err := httpProxySrv.ListenAndServe(); err != nil { //nolint
		print(fmt.Sprintf("Error serving http proxy: %v\n", err))
	}
}
