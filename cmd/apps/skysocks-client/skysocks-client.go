/*
proxy client app for skywire visor
*/
package main

import (
	"flag"
	"io"
	"net"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/internal/netutil"
	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType   = appnet.TypeSkynet
	socksPort = routing.Port(3)
)

var log = logrus.New()

var r = netutil.NewRetrier(time.Second, 0, 1)

func dialServer(appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	var conn net.Conn
	err := r.Do(func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   socksPort,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func main() {
	appC := app.NewClient(nil)
	defer appC.Close()

	skysocks.Log = log

	if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
		log.Printf("Failed to output build info: %v", err)
	}

	var addr = flag.String("addr", skyenv.SkysocksClientAddr, "Client address to listen on")
	var serverPK = flag.String("srv", "", "PubKey of the server to connect to")
	flag.Parse()

	if *serverPK == "" {
		log.Warn("Empty server PubKey. Exiting")
		return
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(*serverPK)); err != nil {
		log.Fatal("Invalid server PubKey: ", err)
	}

	for {
		conn, err := dialServer(appC, pk)
		if err != nil {
			log.Fatalf("Failed to dial to a server: %v", err)
		}

		log.Printf("Connected to %v\n", pk)

		client, err := skysocks.NewClient(conn)
		if err != nil {
			log.Fatal("Failed to create a new client: ", err)
		}

		log.Printf("Serving proxy client %v\n", *addr)

		if err := client.ListenAndServe(*addr); err != nil {
			log.Errorf("Error serving proxy client: %v\n", err)
		}

		// need to filter this out, cause usually client failure means app conn is already closed
		if err := conn.Close(); err != nil && err != io.ErrClosedPipe {
			log.Errorf("Error closing app conn: %v\n", err)
		}

		log.Println("Reconnecting to skysocks server")
	}
}
