// /* cmd/apps/skyhttp/skyhttp.go
/*
http proxy client app for skysocks-client
*/
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"

	"github.com/elazarl/goproxy"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func main() {
	appCl := app.NewClient(nil)
	defer appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	var addr = flag.String("addr", visorconfig.SkyHTTPAddr, "skyhttp address to listen on")
	var socks = flag.String("socks", "", "address of socks proxy")
	flag.Parse()

	if *socks == "" {
		err := errors.New("Empty socks address. Exiting")
		print(fmt.Sprintf("%v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(visorconfig.SkychatName, nil)
		if err != nil {
			print(fmt.Sprintf("Error creating ipc server for skychat client: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
		go handleIPCSignal(ipcClient)
	}

	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh
			setAppStatus(appCl, appserver.AppDetailedStatusStopped)
			os.Exit(1)
		}()
	}

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)
	setAppPort(appCl, appCl.Config().RoutingPort)
	for {
		proxy := goproxy.NewProxyHttpServer()
		proxyURL, err := url.Parse(fmt.Sprintf("socks5://%s", *socks)) //nolint
		if err != nil {
			print(fmt.Sprintf("Failed to parse socks address: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
		proxy.Tr.Proxy = http.ProxyURL(proxyURL)
		proxy.Verbose = true
		fmt.Printf("Serving http proxy %v\n", *addr)
		setAppStatus(appCl, appserver.AppDetailedStatusRunning)

		if err := http.ListenAndServe(*addr, proxy); err != nil { //nolint
			print(fmt.Sprintf("Error serving http proxy: %v\n", err))
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

func handleIPCSignal(client *ipc.Client) {
	for {
		m, err := client.Read()
		if err != nil {
			fmt.Printf("%s IPC received error: %v", visorconfig.SkyHTTPName, err)
		}
		if m.MsgType == visorconfig.IPCShutdownMessageType {
			fmt.Println("Stopping " + visorconfig.SkyHTTPName + " via IPC")
			break
		}
	}
	os.Exit(0)
}
