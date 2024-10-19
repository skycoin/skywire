// Package commands cmd/dmsg-socks5/commands/dmsg-socks5.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	socks5 "github.com/confiant-inc/go-socks5"
	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	sk        cipher.SecKey
	pubk      string
	dmsgDisc  string
	wl        string
	wlkeys    []cipher.PubKey
	proxyPort int
	dmsgPort  uint16
)

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
func init() {
	var envServices skywire.EnvServices
	var services skywire.Services
	if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err == nil {
		if err := json.Unmarshal(envServices.Prod, &services); err == nil {
			dmsgDisc = services.DmsgDiscovery
		}
	}
	RootCmd.AddCommand(
		serveCmd,
		proxyCmd,
	)
	serveCmd.Flags().Uint16VarP(&dmsgPort, "dport", "q", 1081, "dmsg port to serve socks5")
	serveCmd.Flags().StringVarP(&wl, "wl", "w", "", "whitelist keys, comma separated")
	serveCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDisc, "dmsg discovery url")
	if os.Getenv("DMSGSK") != "" {
		sk.Set(os.Getenv("DMSGSK")) //nolint
	}
	serveCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")

	proxyCmd.Flags().IntVarP(&proxyPort, "port", "p", 1081, "TCP port to serve SOCKS5 proxy locally")
	proxyCmd.Flags().Uint16VarP(&dmsgPort, "dport", "q", 1081, "dmsg port to connect to socks5 server")
	proxyCmd.Flags().StringVarP(&pubk, "pk", "k", "", "dmsg socks5 proxy server public key to connect to")
	proxyCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDisc, "dmsg discovery url")
	if os.Getenv("DMSGSK") != "" {
		sk.Set(os.Getenv("DMSGSK")) //nolint
	}
	proxyCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")

}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG socks5 proxy server & client",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┌─┐┬┌─┌─┐
	 │││││└─┐│ ┬───└─┐│ ││  ├┴┐└─┐
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘└─┘┴ ┴└─┘
DMSG socks5 proxy server & client`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// serveCmd serves socks5 over dmsg
var serveCmd = &cobra.Command{
	Use:                   "server",
	Short:                 "dmsg socks5 proxy server",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		log := logging.MustGetLogger("ssh-proxy")
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		go func() {
			<-interrupt
			log.Info("Interrupt received. Shutting down...")
			os.Exit(0)
		}()
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		if wl != "" {
			wlk := strings.Split(wl, ",")
			for _, key := range wlk {
				var pk1 cipher.PubKey
				err := pk1.Set(key)
				if err == nil {
					wlkeys = append(wlkeys, pk1)
				}
			}
		}
		if len(wlkeys) > 0 {
			if len(wlkeys) == 1 {
				log.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
			} else {
				log.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
			}
		}
		//TODO: implement whitelist logic
		respC := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), dmsg.DefaultConfig())
		go respC.Serve(context.Background())
		log.Infof("dmsg client pk: " + pk.String())
		time.Sleep(time.Second)
		respL, err := respC.Listen(dmsgPort)
		if err != nil {
			log.Fatalf("Error listening on port %d: %v", dmsgPort, err)
		}
		defer func() {
			if err := respL.Close(); err != nil {
				log.Printf("Error closing listener: %v", err)
			}
		}()
		defer func() {
			if err := respC.Close(); err != nil {
				log.Errorf("Error closing DMSG client: %v", err)
			}
		}()
		for {
			respConn, err := respL.Accept()
			if err != nil {
				log.Errorf("Error accepting initiator: %v", err)
				continue
			}
			log.Infof("Accepted connection from: %s", respConn.RemoteAddr())

			conf := &socks5.Config{}
			server, err := socks5.New(conf)
			if err != nil {
				log.Fatalf("Error creating SOCKS5 server: %v", err)
			}
			go func() {
				defer func() {
					if closeErr := respConn.Close(); closeErr != nil {
						log.Printf("Error closing client connection: %v", closeErr)
					}
				}()
				if err := server.ServeConn(respConn); err != nil {
					log.Infof("Connection closed: %s", respConn.RemoteAddr())
					log.Errorf("Error serving SOCKS5 proxy: %v", err)
				}
			}()
		}
	},
}

// proxyCmd serves the local socks5 proxy
var proxyCmd = &cobra.Command{
	Use:   "client",
	Short: "socks5 proxy client for dmsg socks5 proxy server",
	Run: func(_ *cobra.Command, _ []string) {
		log := logging.MustGetLogger("ssh-proxy-client")
		var pubKey cipher.PubKey
		err := pubKey.Set(pubk)
		if err != nil {
			log.Fatal("Public key to connect to cannot be empty")
		}
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		initC := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), dmsg.DefaultConfig())
		go initC.Serve(context.Background())
		initL, err := initC.Listen(dmsgPort)
		if err != nil {
			log.Fatalf("Error listening by initiator on port %d: %v", dmsgPort, err)
		}
		defer func() {
			if err := initL.Close(); err != nil {
				log.Printf("Error closing initiator's listener: %v", err)
			}
		}()
		log.Infof("Socks5 proxy client connected on DMSG port %d", dmsgPort)
		initTp, err := initC.DialStream(context.Background(), dmsg.Addr{PK: pubKey, Port: dmsgPort})
		if err != nil {
			log.Fatalf("Error dialing responder: %v", err)
		}
		defer func() {
			if err := initTp.Close(); err != nil {
				log.Printf("Error closing initiator's stream: %v", err)
			}
		}()
		conf := &socks5.Config{}
		server, err := socks5.New(conf)
		if err != nil {
			log.Fatalf("Error creating SOCKS5 server: %v", err)
		}
		proxyListenAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
		log.Infof("Serving SOCKS5 proxy on %s", proxyListenAddr)
		if err := server.ListenAndServe("tcp", proxyListenAddr); err != nil {
			log.Fatalf("Error serving SOCKS5 proxy: %v", err)
		}
	},
}
