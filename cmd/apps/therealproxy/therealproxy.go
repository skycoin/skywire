/*
proxy server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/internal/therealproxy"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

const (
	appName = "socksproxy"
	netType = appnet.TypeSkynet
	port    = routing.Port(3)
)

func main() {
	log := app.NewLogger(appName)
	therealproxy.Log = log.PackageLogger("therealproxy")

	var passcode = flag.String("passcode", "", "Authorize user against this passcode")
	flag.Parse()

	config, err := app.ClientConfigFromEnv()
	if err != nil {
		log.Fatalf("Error getting client config: %v\n", err)
	}

	socksApp, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), config)
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer func() {
		socksApp.Close()
	}()

	srv, err := therealproxy.NewServer(*passcode, log)
	if err != nil {
		log.Fatal("Failed to create a new server: ", err)
	}

	l, err := socksApp.Listen(netType, port)
	if err != nil {
		log.Fatalf("Error listening network %v on port %d: %v\n", netType, port, err)
	}

	log.Infoln("Starting serving proxy server")

	log.Fatal(srv.Serve(l))
}
