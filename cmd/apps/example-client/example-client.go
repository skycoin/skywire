// /* cmd/apps/skysocks-client/skysocks-client.go
/*
proxy client app for skywire visor
*/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	netType = appnet.TypeSkynet
)

var r = netutil.NewRetrier(nil, time.Second, netutil.DefaultMaxBackoff, 0, 1)

func dialServer(ctx context.Context, appCl *app.Client, hostAddr routing.Addr) (net.Conn, error) {
	var conn net.Conn
	err := r.Do(ctx, func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: hostAddr.PubKey,
			Port:   hostAddr.Port,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func main() {
	appCl := app.NewClient(nil)
	defer appCl.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
		print(fmt.Sprintf("Failed to output build info: %v\n", err))
	}

	osSigs := make(chan os.Signal, 2)

	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	var serverAddr = flag.String("addr", "", "PubKey and port of the server to connect to")
	flag.Parse()
	var hostAddr routing.Addr
	if err := hostAddr.Set(*serverAddr); err != nil {
		print(fmt.Sprintf("invalid host address: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)
	conn, err := dialServer(ctx, appCl, hostAddr)
	if err != nil {
		print(fmt.Sprintf("Failed to dial to a server: %v\n", err))
		setAppErr(appCl, err)
		os.Exit(1)
	}

	fmt.Printf("Connected to %v\n", hostAddr.PubKey)
	setAppStatus(appCl, appserver.AppDetailedStatusRunning)
	helloMsg := "hello"
	_, err = conn.Write([]byte(helloMsg))
	if err != nil {
		print(fmt.Sprintf("error sending data: %v\n", err))
	}
	go handleConn(conn)
	<-osSigs
	err = conn.Close()
	if err != nil {
		print(fmt.Sprintf("Failed to close conn: %v\n", err))
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

func handleConn(conn net.Conn) {
	rAddr := conn.RemoteAddr().(appnet.Addr)
	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println("Failed to read packet:", err)
			return
		}

		servMsg, err := json.Marshal(map[string]string{"sender": rAddr.PubKey.Hex(), "message": string(buf[:n])})
		if err != nil {
			print(fmt.Sprintf("Failed to marshal json: %v\n", err))
		}
		fmt.Printf("Received: %s\n", servMsg)
	}
}
