/*
proxy server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/therealproxy"
	"github.com/skycoin/skywire/pkg/app2"
	"github.com/skycoin/skywire/pkg/app2/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	netType = appnet.TypeDMSG
	port    = routing.Port(1024)
)

func main() {
	appName := "socksproxy"

	log := app2.NewLogger(appName)
	therealproxy.Log = log.PackageLogger("therealproxy")

	var passcode = flag.String("passcode", "", "Authorize user against this passcode")
	flag.Parse()

	config, err := app2.ClientConfigFromEnv()
	if err != nil {
		log.Fatalf("Error getting client config: %v\n", err)
	}

	socksApp, err := app2.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), config)
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

	log.Fatal(srv.Serve(l))
}
