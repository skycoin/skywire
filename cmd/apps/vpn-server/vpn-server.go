package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/SkycoinProject/skywire-mainnet/internal/vpn"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

const (
	appName = "vpn-server"
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(44)
)

var (
	log = app.NewLogger(appName)
)

func main() {
	defaultNetworkIfc, err := vpn.DefaultNetworkIfc()
	if err != nil {
		log.WithError(err).Fatalln("Error getting default network interface")
	}

	log.Infof("Got default network interface: %s", defaultNetworkIfc)

	ipv4ForwardingVal, err := vpn.GetIPv4ForwardingValue()
	if err != nil {
		log.WithError(err).Fatalln("Error getting IPv4 forwarding value")
	}
	ipv6ForwardingVal, err := vpn.GetIPv6ForwardingValue()
	if err != nil {
		log.WithError(err).Fatalln("Error getting IPv6 forwarding value")
	}

	log.Infoln("Old IP forwarding values:")
	log.Infof("IPv4: %s, IPv6: %s", ipv4ForwardingVal, ipv6ForwardingVal)

	if err := vpn.EnableIPv4Forwarding(); err != nil {
		log.WithError(err).Errorln("Error enabling IPv4 forwarding")
		return
	}
	log.Infoln("Set IPv4 forwarding = 1")
	defer func() {
		if err := vpn.SetIPv4ForwardingValue(ipv4ForwardingVal); err != nil {
			log.WithError(err).Errorln("Error reverting IPv4 forwarding")
		} else {
			log.Infof("Set IPv4 forwarding = %s", ipv4ForwardingVal)
		}
	}()

	if err := vpn.EnableIPv6Forwarding(); err != nil {
		log.WithError(err).Errorln("Error enabling IPv6 forwarding")
		return
	}
	log.Infoln("Set IPv6 forwarding = 1")
	defer func() {
		if err := vpn.SetIPv6ForwardingValue(ipv6ForwardingVal); err != nil {
			log.WithError(err).Errorln("Error reverting IPv6 forwarding")
		} else {
			log.Infof("Set IPv6 forwarding = %s", ipv6ForwardingVal)
		}
	}()

	if err := vpn.EnableIPMasquerading(defaultNetworkIfc); err != nil {
		log.WithError(err).Errorf("Error enabling IP masquerading for %s", defaultNetworkIfc)
		return
	}

	log.Infoln("Enabled IP masquerading")

	defer func() {
		if err := vpn.DisableIPMasquerading(defaultNetworkIfc); err != nil {
			log.WithError(err).Errorf("Error disabling IP masquerading for %s", defaultNetworkIfc)
		} else {
			log.Infoln("Disabled IP masquerading")
		}
	}()

	appCfg, err := app.ClientConfigFromEnv()
	if err != nil {
		log.WithError(err).Errorln("Error getting app client config")
		return
	}

	appClientt, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), appCfg)
	if err != nil {
		log.WithError(err).Errorln("Error setting up VPN client")
		return
	}
	defer func() {
		appClientt.Close()
	}()

	osSigs := make(chan os.Signal)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	shutdownC := make(chan struct{})

	go func() {
		<-osSigs

		shutdownC <- struct{}{}
	}()

	l, err := appClientt.Listen(netType, vpnPort)
	if err != nil {
		log.WithError(err).Errorf("Error listening network %v on port %d", netType, vpnPort)
		return
	}

	log.Infof("Got app listener, bound to %d", vpnPort)

	// TODO: fix /run to return error

	srv := vpn.NewServer(log)
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

	<-shutdownC
}
