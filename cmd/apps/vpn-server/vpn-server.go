package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/internal/vpn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
)

const (
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(skyenv.VPNServerPort)
)

var (
	log = logrus.New()
)

func main() {
	appClient := app.NewClient()
	defer appClient.Close()

	osSigs := make(chan os.Signal, 2)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	l, err := appClient.Listen(netType, vpnPort)
	if err != nil {
		log.WithError(err).Errorf("Error listening network %v on port %d", netType, vpnPort)
		return
	}

	log.Infof("Got app listener, bound to %d", vpnPort)

	srv, err := vpn.NewServer(log)
	if err != nil {
		log.WithError(err).Fatalln("Error creating VPN server")
	}
	defer func() {
		if err := srv.Close(); err != nil {
			log.WithError(err).Errorln("Error closing server")
		}
	}()
	go func() {
		if err := srv.Serve(l); err != nil {
			log.WithError(err).Errorln("Error serving")
		}
	}()

	<-osSigs
}
