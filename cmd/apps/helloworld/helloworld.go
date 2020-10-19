/*
simple client server app for skywire visor testing
*/
package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	modeServer = "server"
	modeClient = "client"
)

var (
	mode    = flag.String("mode", modeServer, fmt.Sprintf("mode of operation: %v", []string{modeServer, modeClient}))
	network = flag.String("net", string(appnet.TypeSkynet), fmt.Sprintf("network: %v", []appnet.Type{appnet.TypeSkynet, appnet.TypeDmsg}))
	remote  = flag.String("remote", "", "remote public key to dial to (client mode only)")
	port    = flag.Uint("port", 1024, "port to either dial to (client mode), or listen from (server mode)")
)

var log = logrus.New()

func main() {
	flag.Parse()

	subs := prepareSubscriptions()
	appC := app.NewClient(subs)
	defer appC.Close()

	if _, err := buildinfo.Get().WriteTo(log.Writer()); err != nil {
		log.WithError(err).Info("Failed to output build info.")
	}

	switch *mode {
	case modeServer:
		runServer(appC)
	case modeClient:
		runClient(appC)
	default:
		log.WithField("mode", *mode).Fatal("Invalid mode.")
	}
}

func prepareSubscriptions() *appevent.Subscriber {
	subs := appevent.NewSubscriber()

	subs.OnTCPDial(func(data appevent.TCPDialData) {
		log.WithField("event_type", data.Type()).
			WithField("event_data", data).
			Info("Received event.")
	})

	subs.OnTCPClose(func(data appevent.TCPCloseData) {
		log.WithField("event_type", data.Type()).
			WithField("event_data", data).
			Info("Received event.")
	})

	return subs
}

func runServer(appC *app.Client) {
	log := log.
		WithField("network", *network).
		WithField("port", *port)

	lis, err := appC.Listen(appnet.Type(*network), routing.Port(*port))
	if err != nil {
		log.WithError(err).Fatal("Failed to listen.")
	}
	log.Info("Listening for incoming connections.")

	for {
		conn, err := lis.Accept()
		if err != nil {
			log.WithError(err).Fatal("Failed to accept connection.")
		}
		go handleServerConn(log, conn)
	}
}

func handleServerConn(log logrus.FieldLogger, conn net.Conn) {
	log = log.WithField("remote_addr", conn.RemoteAddr())
	log.Info("Serving connection.")
	defer func() {
		log.WithError(conn.Close()).Debug("Closed connection.")
	}()

	for {
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			log.WithField("n", n).WithError(err).
				Error("Failed to read from connection.")
			return
		}
		msg := string(buf[:n])
		log.WithField("n", n).WithField("data", msg).Info("Read from connection.")

		n, err = conn.Write([]byte(fmt.Sprintf("I've got your message: %s", msg)))
		if err != nil {
			log.WithField("n", n).WithError(err).
				Error("Failed to write to connection.")
			return
		}
		log.WithField("n", n).Info("Wrote response message.")
	}
}

func runClient(appC *app.Client) {
	var remotePK cipher.PubKey
	if err := remotePK.UnmarshalText([]byte(*remote)); err != nil {
		log.WithError(err).Fatal("Invalid remote public key.")
	}

	var conn net.Conn

	for i := 0; true; i++ {
		time.Sleep(time.Second * 2)

		if conn != nil {
			log.WithError(conn.Close()).Debug("Connection closed.")
			conn = nil
		}

		var err error
		conn, err = appC.Dial(appnet.Addr{
			Net:    appnet.Type(*network),
			PubKey: remotePK,
			Port:   routing.Port(*port),
		})
		if err != nil {
			log.WithError(err).Error("Failed to dial.")
			time.Sleep(time.Second)
			continue
		}

		n, err := conn.Write([]byte(fmt.Sprintf("Hello world! %d", i)))
		if err != nil {
			log.WithField("n", n).WithError(err).
				Error("Failed to write to connection.")
			continue
		}

		buf := make([]byte, 1024)
		n, err = conn.Read(buf)
		if err != nil {
			log.WithField("n", n).WithError(err).
				Error("Failed to read from connection.")
			continue
		}
		msg := string(buf[:n])
		log.WithField("n", n).WithField("data", msg).Info("Read reply from connection.")
	}
}
