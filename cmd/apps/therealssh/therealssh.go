/*
ssh server app for skywire visor
*/
package main

import (
	"flag"
	"fmt"

	"github.com/mitchellh/go-homedir"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	ssh "github.com/skycoin/skywire/pkg/therealssh"

	"github.com/skycoin/skywire/pkg/app2"
	"github.com/skycoin/skywire/pkg/app2/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

var log *logging.MasterLogger

const (
	netType = appnet.TypeDMSG
	port    = routing.Port(1000)
)

func main() {
	appName := "SSH"

	log = app2.NewLogger(appName)
	ssh.Log = log.PackageLogger("therealssh")

	var authFile = flag.String("auth", "~/.therealssh/authorized_keys", "Auth file location. Should contain one PubKey per line.")
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

	path, err := homedir.Expand(*authFile)
	if err != nil {
		log.Fatal("Failed to resolve auth file path: ", err)
	}

	ssh.Debug = *debug
	if !ssh.Debug {
		logging.SetLevel(logrus.InfoLevel)
	}

	auth, err := ssh.NewFileAuthorizer(path)
	if err != nil {
		log.Fatal("Failed to setup Authorizer: ", err)
	}

	server := ssh.NewServer(auth, log)
	defer func() {
		if err := server.Close(); err != nil {
			log.Println("Failed to close server:", err)
		}
	}()

	l, err := sshApp.Listen(netType, port)
	if err != nil {
		log.Fatalf("Error listening network %v on port %d: %v\n", netType, port, err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal("failed to receive packet: ", err)
		}

		go func() {
			if err := server.Serve(conn); err != nil {
				log.Println("Failed to serve conn:", err)
			}
		}()
	}
}
