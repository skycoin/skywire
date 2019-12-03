/*
proxy client app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/internal/netutil"
	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/internal/therealproxy"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

const (
	appName   = "socksproxy-client"
	netType   = appnet.TypeSkynet
	socksPort = routing.Port(3)
)

var r = netutil.NewRetrier(time.Second, 0, 1)

func main() {
	log := app.NewLogger(appName)
	therealproxy.Log = log.PackageLogger("therealproxy")

	var addr = flag.String("addr", skyenv.SkyproxyClientAddr, "Client address to listen on")
	var serverPK = flag.String("srv", "", "PubKey of the server to connect to")
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

	if *serverPK == "" {
		log.Fatal("Invalid server PubKey")
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(*serverPK)); err != nil {
		log.Fatal("Invalid server PubKey: ", err)
	}

	var conn net.Conn
	err = r.Do(func() error {
		conn, err = socksApp.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   socksPort,
		})
		return err
	})
	if err != nil {
		log.Fatal("Failed to dial to a server: ", err)
	}

	log.Printf("Connected to %v\n", pk)

	client, err := therealproxy.NewClient(conn)
	if err != nil {
		log.Fatal("Failed to create a new client: ", err)
	}

	log.Printf("Serving proxy client %v\n", *addr)

	if err := client.ListenAndServe(*addr); err != nil {
		log.Fatalf("Error serving proxy client: %v\n", err)
	}
}
