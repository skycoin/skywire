/*
ssh client app for skywire visor
*/
package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app2"
	ssh "github.com/skycoin/skywire/pkg/therealssh"
)

const (
	appName = "SSH-client"
)

var log *logging.MasterLogger

func main() {
	log = app2.NewLogger(appName)
	ssh.Log = log.PackageLogger("therealssh")

	var rpcAddr = flag.String("rpc", ":2222", "Client RPC address to listen on")
	var debug = flag.Bool("debug", false, "enable debug messages")
	flag.Parse()

	config, err := app2.ClientConfigFromEnv()
	if err != nil {
		log.Fatalf("Error getting client config: %v\n", err)
	}

	sshApp, err := app2.NewClient(logging.MustGetLogger(fmt.Sprintf("app_%s", appName)), config)
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer func() {
		sshApp.Close()
	}()

	ssh.Debug = *debug
	if !ssh.Debug {
		logging.SetLevel(logrus.InfoLevel)
	}

	rpc, client, err := ssh.NewClient(*rpcAddr, sshApp)
	if err != nil {
		log.Fatal("Client setup failure: ", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Println("Failed to close client:", err)
		}
	}()

	if err := http.Serve(rpc, nil); err != nil {
		log.Fatal("Failed to start RPC interface: ", err)
	}
}
