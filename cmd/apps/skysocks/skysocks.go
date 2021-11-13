/*
proxy server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"os"
	"os/signal"
	"runtime"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/buildinfo"

	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	netType              = appnet.TypeSkynet
	port    routing.Port = 3
)

var log = logrus.New()

func main() {
	appC := app.NewClient(nil)
	defer appC.Close()

	skysocks.Log = log

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		fmt.Printf("Failed to output build info: %v", err)
	}

	var passcode = flag.String("passcode", "", "Authorize user against this passcode")
	flag.Parse()

	srv, err := skysocks.NewServer(*passcode, log)
	if err != nil {
		log.Fatal("Failed to create a new server: ", err)
	}

	l, err := appC.Listen(netType, port)
	if err != nil {
		log.Fatalf("Error listening network %v on port %d: %v\n", netType, port, err)
	}

	fmt.Println("Starting serving proxy server")

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(skyenv.VPNClientName, nil)
		if err != nil {
			fmt.Printf("Error creating ipc server for VPN client: %v\n", err)
			os.Exit(1)
		}
		go srv.ListenIPC(ipcClient)
	} else {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			<-termCh

			if err := srv.Close(); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}()

	}

	if err := srv.Serve(l); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
