/*
simple client server app for skywire visor testing
*/
package main

import (
	"os"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/buildinfo"
)

const (
	netType = appnet.TypeSkynet
)

var log = logrus.New()

func main() {
	appC := app.NewClient()
	defer appC.Close()

	if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
		log.Printf("Failed to output build info: %v", err)
	}

	if len(os.Args) == 1 {
		port := routing.Port(1024)
		l, err := appC.Listen(netType, port)
		if err != nil {
			log.Fatalf("Error listening network %v on port %d: %v\n", netType, port, err)
		}

		log.Println("listening for incoming connections")
		for {
			conn, err := l.Accept()
			if err != nil {
				log.Fatalf("Failed to accept conn: %v\n", err)
			}

			log.Printf("got new connection from: %v\n", conn.RemoteAddr())
			go func() {
				buf := make([]byte, 4)
				if _, err := conn.Read(buf); err != nil {
					log.Printf("Failed to read remote data: %v\n", err)
					// TODO: close conn
				}

				log.Printf("Message from %s: %s\n", conn.RemoteAddr().String(), string(buf))
				if _, err := conn.Write([]byte("pong")); err != nil {
					log.Printf("Failed to write to a remote visor: %v\n", err)
					// TODO: close conn
				}
			}()
		}
	}

	remotePK := cipher.PubKey{}
	if err := remotePK.UnmarshalText([]byte(os.Args[1])); err != nil {
		log.Fatal("Failed to construct PubKey: ", err, os.Args[1])
	}

	conn, err := appC.Dial(appnet.Addr{
		Net:    netType,
		PubKey: remotePK,
		Port:   10,
	})
	if err != nil {
		log.Fatalf("Failed to open remote conn: %v\n", err)
	}

	if _, err := conn.Write([]byte("ping")); err != nil {
		log.Fatalf("Failed to write to a remote visor: %v\n", err)
	}

	buf := make([]byte, 4)
	if _, err = conn.Read(buf); err != nil {
		log.Fatalf("Failed to read remote data: %v\n", err)
	}

	log.Printf("Message from %s: %s", conn.RemoteAddr().String(), string(buf))
}
