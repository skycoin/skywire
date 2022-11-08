// /* cmd/apps/skysocks/skysocks.go
/*
proxy server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType              = appnet.TypeSkynet
	port    routing.Port = 3
)

func main() {
	appCl := app.NewClient(nil, nil, nil)
	defer appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v", err))
	}

	var passcode = flag.String("passcode", "", "Authorize user against this passcode")
	flag.Parse()

	srv, err := skysocks.NewServer(*passcode, appCl)
	if err != nil {
		setAppError(appCl, err)
		print(fmt.Sprintf("Failed to create a new server: %v\n", err))
		os.Exit(1)
	}

	l, err := appCl.Listen(netType, port)
	if err != nil {
		setAppError(appCl, err)
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, port, err))
		os.Exit(1)
	}

	fmt.Println("Starting serving proxy server")

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(skyenv.VPNClientName, nil)
		if err != nil {
			setAppError(appCl, err)
			print(fmt.Sprintf("Error creating ipc server for VPN client: %v\n", err))
			os.Exit(1)
		}
		go srv.ListenIPC(ipcClient)
	} else {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh

			if err := srv.Close(); err != nil {
				print(fmt.Sprintf("%v\n", err))
				os.Exit(1)
			}
		}()
	}
	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	if err := srv.Serve(l); err != nil {
		print(fmt.Sprintf("%v\n", err))
		os.Exit(1)
	}
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", appErr, err))
	}
}
