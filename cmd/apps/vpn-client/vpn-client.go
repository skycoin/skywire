package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/netutil"

	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(skyenv.VPNServerPort)
)

const (
	serverDialInitBO = 1 * time.Second
	serverDialMaxBO  = 10 * time.Second
)

var (
	log = logrus.New()
	r   = netutil.NewRetrier(log, serverDialInitBO, serverDialMaxBO, 0, 1)
)

var (
	serverPKStr = flag.String("srv", "", "PubKey of the server to connect to")
	localPKStr  = flag.String("pk", "", "Local PubKey")
	localSKStr  = flag.String("sk", "", "Local SecKey")
	passcode    = flag.String("passcode", "", "Passcode to authenticate connection")
)

func main() {
	flag.Parse()

	if *serverPKStr == "" {
		// TODO(darkrengarius): fix args passage for Windows
		//*serverPKStr = "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d"
		fmt.Println("VPN server pub key is missing")
		os.Exit(1)
	}

	serverPK := cipher.PubKey{}
	if err := serverPK.UnmarshalText([]byte(*serverPKStr)); err != nil {
		fmt.Printf("Invalid VPN server pub key: %v\n", err)
		os.Exit(1)
	}

	localPK := cipher.PubKey{}
	if *localPKStr != "" {
		if err := localPK.UnmarshalText([]byte(*localPKStr)); err != nil {
			fmt.Printf("Invalid local PK: %v\n", err)
			os.Exit(1)
		}
	}

	localSK := cipher.SecKey{}
	if *localSKStr != "" {
		if err := localSK.UnmarshalText([]byte(*localSKStr)); err != nil {
			fmt.Printf("Invalid local SK: %v\n", err)
			os.Exit(1)
		}
	}

	var directIPsCh, nonDirectIPsCh = make(chan net.IP, 100), make(chan net.IP, 100)
	defer close(directIPsCh)
	defer close(nonDirectIPsCh)

	eventSub := appevent.NewSubscriber()

	parseIP := func(addr string) net.IP {
		ip, ok, err := vpn.ParseIP(addr)
		if err != nil {
			fmt.Printf("Failed to parse IP %s: %v\n", addr, err)
			return nil
		}
		if !ok {
			fmt.Printf("Failed to parse IP %s\n", addr)
			return nil
		}

		return ip
	}

	eventSub.OnTCPDial(func(data appevent.TCPDialData) {
		if ip := parseIP(data.RemoteAddr); ip != nil {
			directIPsCh <- ip
		}
	})

	eventSub.OnTCPClose(func(data appevent.TCPCloseData) {
		if ip := parseIP(data.RemoteAddr); ip != nil {
			nonDirectIPsCh <- ip
		}
	})

	appClient := app.NewClient(eventSub)
	defer appClient.Close()

	fmt.Printf("Connecting to VPN server %s\n", serverPK.String())

	appConn, err := dialServer(appClient, serverPK)
	if err != nil {
		fmt.Println("Error connecting to VPN server")
		os.Exit(1)
	}
	defer func() {
		if err := appConn.Close(); err != nil {
			fmt.Printf("Error closing connection to the VPN server: %v\n", err)
		}
	}()

	fmt.Printf("Dialed %s\n", appConn.RemoteAddr())

	vpnClientCfg := vpn.ClientConfig{
		Passcode: *passcode,
	}
	vpnClient, err := vpn.NewClient(vpnClientCfg, appClient, appConn)
	if err != nil {
		fmt.Printf("Error creating VPN client: %v\n", err)
	}

	osSigs := make(chan os.Signal, 2)
	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	go func() {
		<-osSigs
		vpnClient.Close()
	}()

	var directRoutesDone bool
	for !directRoutesDone {
		select {
		case ip := <-directIPsCh:
			if err := vpnClient.AddDirectRoute(ip); err != nil {
				fmt.Printf("Failed to setup direct route to %s: %v\n", ip.String(), err)
			}
		default:
			directRoutesDone = true
		}
	}

	go func() {
		for ip := range directIPsCh {
			if err := vpnClient.AddDirectRoute(ip); err != nil {
				fmt.Printf("Failed to setup direct route to %s: %v\n", ip.String(), err)
			}
		}
	}()

	go func() {
		for ip := range nonDirectIPsCh {
			if err := vpnClient.RemoveDirectRoute(ip); err != nil {
				fmt.Printf("Failed to remove direct route to %s: %v\n", ip.String(), err)
			}
		}
	}()

	if err := vpnClient.Serve(); err != nil {
		fmt.Printf("Error serving VPN: %v\n", err)
	}
}

func dialServer(appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	var conn net.Conn
	err := r.Do(context.Background(), func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   vpnPort,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}
