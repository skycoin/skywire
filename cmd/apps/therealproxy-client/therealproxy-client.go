/*
proxy client app for skywire visor
*/
package main

import (
	"flag"
	"net"

	"github.com/SkycoinProject/dmsg/cipher"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/internal/therealproxy"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func main() {
	log := app.NewLogger(skyenv.SkyproxyClientName)
	therealproxy.Log = log.PackageLogger(skyenv.SkyproxyClientName)

	var addr = flag.String("addr", skyenv.SkyproxyClientAddr, "Client address to listen on")
	var serverPK = flag.String("srv", "", "PubKey of the server to connect to")
	flag.Parse()

	config := &app.Config{AppName: skyenv.SkyproxyClientName, AppVersion: skyenv.SkyproxyClientVersion, ProtocolVersion: skyenv.AppProtocolVersion}
	socksApp, err := app.Setup(config)
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer func() {
		if err := socksApp.Close(); err != nil {
			log.Println("Failed to close app:", err)
		}
	}()

	if *serverPK == "" {
		log.Fatal("Invalid server PubKey")
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(*serverPK)); err != nil {
		log.Fatal("Invalid server PubKey: ", err)
	}

	log.Printf("Serving on %v", *addr)
	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("Failed to listen on %v: %v", *addr, err)
	}

	remote := routing.Addr{PubKey: pk, Port: routing.Port(skyenv.SkyproxyPort)}

	client, err := therealproxy.NewClient(l, socksApp, remote)
	if err != nil {
		log.Fatal("Failed to create a new client: ", err)
	}

	if err := client.Serve(); err != nil {
		log.Warnf("Failed to serve: %v", err)
	}
}
