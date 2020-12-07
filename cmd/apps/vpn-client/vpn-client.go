package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
)

var (
	serverPKStr = flag.String("srv", "", "PubKey of the server to connect to")
	localPKStr  = flag.String("pk", "", "Local PubKey")
	localSKStr  = flag.String("sk", "", "Local SecKey")
	passcode    = flag.String("passcode", "", "Passcode to authenticate connection")
	killswitch  = flag.Bool("killswitch", false, "If set, the Internet won't be restored during reconnection attempts")
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

	vpnClientCfg := vpn.ClientConfig{
		Passcode:   *passcode,
		Killswitch: *killswitch,
		ServerPK:   serverPK,
	}
	vpnClient, err := vpn.NewClient(vpnClientCfg, appClient)
	if err != nil {
		fmt.Printf("Error creating VPN client: %v\n", err)
	}

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

	osSigs := make(chan os.Signal, 2)
	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	go func() {
		<-osSigs
		vpnClient.Close()
	}()

	f, err := os.Create("./cpu.pprof")
	if err != nil {
		log.Fatalf("Error creating CPU profile: %v\n", err)
	}

	if err := pprof.StartCPUProfile(f); err != nil {
		log.Fatalf("Error starting CPU profiling: %v\n", err)
	}

	defer pprof.StopCPUProfile()

	if err := vpnClient.Serve(); err != nil {
		fmt.Printf("Failed to serve VPN: %v\n", err)
	}
}
