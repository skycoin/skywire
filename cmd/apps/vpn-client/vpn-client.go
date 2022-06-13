package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/skyenv"
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

	eventSub := appevent.NewSubscriber()
	appCl := app.NewClient(eventSub)
	defer appCl.Close()

	if *serverPKStr == "" {
		// TODO(darkrengarius): fix args passage for Windows
		//*serverPKStr = "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d"
		err := errors.New("VPN server pub key is missing")
		print(err)
		setAppErr(appCl, err)
		os.Exit(1)
	}

	serverPK := cipher.PubKey{}
	if err := serverPK.UnmarshalText([]byte(*serverPKStr)); err != nil {
		print(fmt.Sprintf("Invalid VPN server pub key: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	localPK := cipher.PubKey{}
	if *localPKStr != "" {
		if err := localPK.UnmarshalText([]byte(*localPKStr)); err != nil {
			print(fmt.Sprintf("Invalid local PK: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
	}

	localSK := cipher.SecKey{}
	if *localSKStr != "" {
		if err := localSK.UnmarshalText([]byte(*localSKStr)); err != nil {
			print(fmt.Sprintf("Invalid local SK: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
	}

	var directIPsCh, nonDirectIPsCh = make(chan net.IP, 100), make(chan net.IP, 100)
	defer close(directIPsCh)
	defer close(nonDirectIPsCh)

	parseIP := func(addr string) net.IP {
		ip, ok, err := vpn.ParseIP(addr)
		if err != nil {
			print(fmt.Sprintf("Failed to parse IP %s: %v\n", addr, err))
			return nil
		}
		if !ok {
			print(fmt.Sprintf("Failed to parse IP %s\n", addr))
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

	fmt.Printf("Connecting to VPN server %s\n", serverPK.String())

	vpnClientCfg := vpn.ClientConfig{
		Passcode:   *passcode,
		Killswitch: *killswitch,
		ServerPK:   serverPK,
	}

	vpnClient, err := vpn.NewClient(vpnClientCfg, appCl)
	if err != nil {
		print(fmt.Sprintf("Error creating VPN client: %v\n", err))
		setAppErr(appCl, err)
	}

	var directRoutesDone bool
	for !directRoutesDone {
		select {
		case ip := <-directIPsCh:
			if err := vpnClient.AddDirectRoute(ip); err != nil {
				print(fmt.Sprintf("Failed to setup direct route to %s: %v\n", ip.String(), err))
				setAppErr(appCl, err)
			}
		default:
			directRoutesDone = true
		}
	}

	go func() {
		for ip := range directIPsCh {
			if err := vpnClient.AddDirectRoute(ip); err != nil {
				print(fmt.Sprintf("Failed to setup direct route to %s: %v\n", ip.String(), err))
				setAppErr(appCl, err)
			}
		}
	}()

	go func() {
		for ip := range nonDirectIPsCh {
			if err := vpnClient.RemoveDirectRoute(ip); err != nil {
				print(fmt.Sprintf("Failed to remove direct route to %s: %v\n", ip.String(), err))
				setAppErr(appCl, err)
			}
		}
	}()

	if runtime.GOOS != "windows" {
		osSigs := make(chan os.Signal, 2)
		sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
		for _, sig := range sigs {
			signal.Notify(osSigs, sig)
		}

		go func() {
			<-osSigs
			vpnClient.Close()
		}()
	} else {
		ipcClient, err := ipc.StartClient(skyenv.VPNClientName, nil)
		if err != nil {
			print(fmt.Sprintf("Error creating ipc server for VPN client: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}
		go vpnClient.ListenIPC(ipcClient)
	}

	if err := vpnClient.Serve(); err != nil {
		print(fmt.Sprintf("Failed to serve VPN: %v\n", err))
	}
}

func setAppErr(appCl *app.Client, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		fmt.Printf("Failed to set error %v: %v\n", err, appErr)
	}
}
