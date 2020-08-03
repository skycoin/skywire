/*
proxy server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/util/buildinfo"
)

const (
	appName              = "skysocks"
	netType              = appnet.TypeSkynet
	port    routing.Port = 3
)

func main() {
	log := app.NewLogger(appName)
	skysocks.Log = log.PackageLogger("skysocks")

	if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
		log.Printf("Failed to output build info: %v", err)
	}

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

	srv, err := skysocks.NewServer(*passcode, log)
	if err != nil {
		log.Fatal("Failed to create a new server: ", err)
	}

	l, err := socksApp.Listen(netType, port)
	if err != nil {
		log.Fatalf("Error listening network %v on port %d: %v\n", netType, port, err)
	}

	log.Infoln("Starting serving proxy server")

	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	go func() {
		<-termCh

		if err := srv.Close(); err != nil {
			log.Fatalf("Failed to close server: %v", err)
		}
	}()

	if err := srv.Serve(l); err != nil {
		log.Fatal(err)
	}
}
