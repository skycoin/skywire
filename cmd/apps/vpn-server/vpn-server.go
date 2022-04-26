package main

import (
	"flag"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(skyenv.VPNServerPort)
)

var (
	log = logrus.New()
)

var (
	localPKStr = flag.String("pk", "", "Local PubKey")
	localSKStr = flag.String("sk", "", "Local SecKey")
	passcode   = flag.String("passcode", "", "Passcode to authenticate connecting users")
	networkIfc = flag.String("netifc", "", "Default network interface for multiple available interfaces")
	secure     = flag.Bool("secure", true, "Forbid connections from clients to server local network")
)

func main() {
	if runtime.GOOS != "linux" {
		log.Fatalln("OS is not supported")
	}

	flag.Parse()

	localPK := cipher.PubKey{}
	if *localPKStr != "" {
		if err := localPK.UnmarshalText([]byte(*localPKStr)); err != nil {
			log.WithError(err).Fatalln("Invalid local PK")
		}
	}

	localSK := cipher.SecKey{}
	if *localSKStr != "" {
		if err := localSK.UnmarshalText([]byte(*localSKStr)); err != nil {
			log.WithError(err).Fatalln("Invalid local SK")
		}
	}

	appClient := app.NewClient(nil)
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

	srvCfg := vpn.ServerConfig{
		Passcode:         *passcode,
		Secure:           *secure,
		NetworkInterface: *networkIfc,
	}
	srv, err := vpn.NewServer(srvCfg, log)
	if err != nil {
		log.WithError(err).Fatalln("Error creating VPN server")
	}
	defer func() {
		if err := srv.Close(); err != nil {
			log.WithError(err).Errorln("Error closing server")
		}
	}()

	errCh := make(chan error)
	go func() {
		if err := srv.Serve(l); err != nil {
			errCh <- err
		}

		close(errCh)
	}()

	select {
	case <-osSigs:
	case err := <-errCh:
		log.WithError(err).Errorln("Error serving")
	}
}
