// Package main vpn-server.go
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(skyenv.VPNServerPort)
)

var (
	localPKStr = flag.String("pk", "", "Local PubKey")
	localSKStr = flag.String("sk", "", "Local SecKey")
	passcode   = flag.String("passcode", "", "Passcode to authenticate connecting users")
	networkIfc = flag.String("netifc", "", "Default network interface for multiple available interfaces")
	secure     = flag.Bool("secure", true, "Forbid connections from clients to server local network")
)

func main() {

	appCl := app.NewClient(nil)
	defer appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	if runtime.GOOS != "linux" {
		err := errors.New("OS is not supported")
		print(err)
		setAppErr(appCl, err)
		os.Exit(1)
	}

	flag.Parse()

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

	osSigs := make(chan os.Signal, 2)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	l, err := appCl.Listen(netType, vpnPort)
	if err != nil {
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, vpnPort, err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	fmt.Printf("Got app listener, bound to %d\n", vpnPort)

	srvCfg := vpn.ServerConfig{
		Passcode:         *passcode,
		Secure:           *secure,
		NetworkInterface: *networkIfc,
	}
	srv, err := vpn.NewServer(srvCfg, appCl)
	if err != nil {
		print(fmt.Sprintf("Error creating VPN server: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			print(fmt.Sprintf("Error closing server: %v\n", err))
		}
	}()

	errCh := make(chan error)
	go func() {
		if err := srv.Serve(l); err != nil {
			errCh <- err
		}

		close(errCh)
	}()

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	select {
	case <-osSigs:
	case err := <-errCh:
		print(fmt.Sprintf("Error serving: %v\n", err))
	}
}

func setAppErr(appCl *app.Client, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", err, appErr))
	}
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}
