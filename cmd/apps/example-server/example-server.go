// /* cmd/apps/skysocks/skysocks.go
/*
proxy server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	netType              = appnet.TypeSkynet
	port    routing.Port = 2
)

func main() {
	appCl := app.NewClient(nil)
	defer appCl.Close()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v", err))
	}

	flag.Parse()

	osSigs := make(chan os.Signal, 2)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	l, err := appCl.Listen(netType, port)
	if err != nil {
		setAppError(appCl, err)
		print(fmt.Sprintf("Error listening network %v on port %d: %v\n", netType, port, err))
		os.Exit(1)
	}

	setAppStatus(appCl, appserver.AppDetailedStatusRunning)
	fmt.Println("Starting serving test server")
	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

	go func() {
		<-osSigs
		setAppStatus(appCl, appserver.AppDetailedStatusStopped)
		os.Exit(1)
	}()

	for {
		fmt.Println("Accepting skychat conn...")
		conn, err := l.Accept()
		if err != nil {
			print(fmt.Sprintf("Failed to accept conn: %v\n", err))
			return
		}
		fmt.Println("Accepted skychat conn")

		rAddr := conn.RemoteAddr().(appnet.Addr)
		fmt.Printf("Accepted test-client conn on %s from %s\n", conn.LocalAddr(), rAddr.PubKey)
		var readHello []byte
		n, err := conn.Read(readHello)
		if err != nil {
			print(fmt.Sprintf("Failed to read from conn: %v\n", err))
			return
		}
		print(fmt.Sprintf("read from conn: %v\n", string(readHello[:n])))
	}
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", appErr, err))
	}
}
