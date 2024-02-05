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
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	netType = appnet.TypeSkynet
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
		ipcClient, err := ipc.StartClient(visorconfig.SkyHTTPName, nil)
		if err != nil {
			print(fmt.Sprintf("Error creating ipc server for skyhttp client: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
		go handleIPCSignal(ipcClient)
	} else {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh
			setAppStatus(appCl, appserver.AppDetailedStatusStopped)
			os.Exit(1)
		}()
	}

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	go listenLoop(appCl)

	proxy := goproxy.NewProxyHttpServer()
	proxyURL, err := url.Parse(fmt.Sprintf("socks5://%s", *socks)) //nolint
	if err != nil {
		print(fmt.Sprintf("Failed to parse socks address: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}
	proxy.Tr.Proxy = http.ProxyURL(proxyURL)
	fmt.Printf("Serving http proxy %v\n", *addr)

	setAppStatus(appCl, appserver.AppDetailedStatusRunning)

	if err := http.ListenAndServe(*addr, proxy); err != nil { //nolint
		print(fmt.Sprintf("Error serving http proxy: %v\n", err))
	}

	setAppStatus(appCl, appserver.AppDetailedStatusStopped)
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

func listenLoop(appCl *app.Client) {
	l, err := appCl.Listen(netType, appCl.Config().RoutingPort)
	if err != nil {
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, appCl.Config().RoutingPort, err))
		setAppErr(appCl, err)
		return
	}

	for {
		fmt.Println("Running skyhttp connection to visor...")
		_, err := l.Accept()
		if err != nil {
			print(fmt.Sprintf("Failed on connecting to visor: %v\n", err))
			return
		}
	}
}
