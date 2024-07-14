// Package main example/http-server/server.go
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/example/http-server/html"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/visor"
)

func homepage(w http.ResponseWriter, r *http.Request) { //nolint:all
	p := html.HomepageParams{
		Title:   "Homepage",
		Message: "Hello from Homepage",
	}
	err := html.Homepage(w, p)
	if err != nil {
		http.Error(w, err.Error(), 500)
	}
}

var port int

func main() {
	port = 9080

	log := logging.MustGetLogger("http-example")
	osSigs := make(chan os.Signal, 2)
	sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	for _, sig := range sigs {
		signal.Notify(osSigs, sig)
	}

	http.HandleFunc("/", homepage)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(html.FS()))))
	srv := &http.Server{ //nolint gosec
		Addr:         fmt.Sprintf(":%v", port),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Infof("serving on http://localhost:%v", port)

	go func() {
		err := srv.ListenAndServe()
		if err != nil {
			fmt.Printf("error serving: %v\n", err)
		}
	}()

	rpcClient, err := client()
	if err != nil {
		fmt.Printf("error serving: %v\n", err)
	}

	skyPort := port

	pubInfo, err := rpcClient.Publish(port, skyPort, appnet.HTTP)
	if err != nil {
		log.Errorf("error closing server: %v", err)
	}

	<-osSigs
	err = srv.Close()
	if err != nil {
		log.Errorf("error closing server: %v", err)
	}
	err = rpcClient.Depublish(pubInfo.ID)
	if err != nil {
		log.Errorf("error closing server: %v", err)
	}
}

func client() (visor.API, error) {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", "localhost:3435", rpcDialTimeout)
	if err != nil {
		return nil, err
	}
	logger := logging.MustGetLogger("api")
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0), nil
}
