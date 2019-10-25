/*
proxy client app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/app2/appnet"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app2"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/internal/netutil"
	"github.com/skycoin/skywire/internal/therealproxy"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	netType   = appnet.TypeDMSG
	socksPort = routing.Port(3)
)

var r = netutil.NewRetrier(time.Second, 0, 1)

func main() {
	appName := "socksproxy-client"

	log := app2.NewLogger(appName)
	therealproxy.Log = log.PackageLogger("therealproxy")

	var addr = flag.String("addr", ":1080", "Client address to listen on")
	var serverPK = flag.String("srv", "", "PubKey of the server to connect to")
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

	log.Printf("Serving  %v\n", addr)

	log.Fatal(client.ListenAndServe(*addr))
}
