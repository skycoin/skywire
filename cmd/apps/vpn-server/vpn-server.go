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
		log.Fatalf("Error getting default network interface: %v", err)
	}

	ipv4ForwardingVal, err := vpn.GetIPv4ForwardingValue()
	if err != nil {
		log.Fatalf("Error getting IPv4 forwarding value: %v", err)
	}
	ipv6ForwardingVal, err := vpn.GetIPv6ForwardingValue()
	if err != nil {
		log.Fatalf("Error getting IPv6 forwarding value: %v", err)
	}

	if err := vpn.EnableIPv4Forwarding(); err != nil {
		log.Fatalf("Error enabling IPv4 forwarding: %v", err)
	}
	defer func() {
		if err := vpn.SetIPv4ForwardingValue(ipv4ForwardingVal); err != nil {
			log.WithError(err).Error("Error reverting IPv4 forwarding: %v", err)
		}
	}()

	if err := vpn.EnableIPv6Forwarding(); err != nil {
		log.Fatalf("Error enabling IPv6 forwarding: %v", err)
	}
	defer func() {
		if err := vpn.SetIPv6ForwardingValue(ipv6ForwardingVal); err != nil {
			log.WithError(err).Error("Error reverting IPv6 forwarding: %v", err)
		}
	}()

	if err := vpn.EnableIPMasquerading(defaultNetworkIfc); err != nil {
		log.WithError(err).Fatalf("Error enabling IP masquerading for %s", defaultNetworkIfc)
	}
	defer func() {
		if err := vpn.DisableIPMasquerading(defaultNetworkIfc); err != nil {
			log.WithError(err).Error("Error disabling IP masquerading for %s", defaultNetworkIfc)
		}
	}()

	appCfg, err := app.ClientConfigFromEnv()
	if err != nil {
		log.Fatalf("Error getting app client config: %v", err)
	}

	appClientt, err := app.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), appCfg)
	if err != nil {
		log.Fatalf("Error setting up VPN client: %v", err)
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
		log.Fatalf("Error listening network %v on port %d: %v\n", netType, vpnPort, err)
	}

	srv := vpn.NewServer(log)
	defer func() {
		if err := srv.Close(); err != nil {
			log.WithError(err).Fatalln("Error closing server")
		}
	}()
	go func() {
		if err := srv.Serve(l); err != nil {
			log.WithError(err).Fatalln("Error serving")
		}
	}()

	<-shutdownC
}
