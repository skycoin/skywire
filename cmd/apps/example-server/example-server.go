// /* cmd/apps/skysocks/skysocks.go
/*
proxy server app for skywire visor
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
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
		handleConn(conn)
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

func handleConn(conn net.Conn) {
	rAddr := conn.RemoteAddr().(appnet.Addr)
	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println("Failed to read packet:", err)
			return
		}

		clientMsg, err := json.Marshal(map[string]string{"sender": rAddr.PubKey.Hex(), "message": string(buf[:n])})
		if err != nil {
			print(fmt.Sprintf("Failed to marshal json: %v\n", err))
		}
		fmt.Printf("Received: %s\n", clientMsg)
		if string(buf[:n]) == "hello" {
			helloRsp := "hi"
			_, err = conn.Write([]byte(helloRsp))
			if err != nil {
				print(fmt.Sprintf("error sending data: %v\n", err))
				return
			}
			fmt.Println("Sent hello response")
		}
	}
}
