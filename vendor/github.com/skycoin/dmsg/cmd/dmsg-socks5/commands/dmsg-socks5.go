// Package commands cmd/dmsg-socks5/commands/dmsg-socks5.go
package commands

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	socks5 "github.com/confiant-inc/go-socks5"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/internal/cli"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	sk           cipher.SecKey
	pubk         string
	wl           string
	wlkeys       []cipher.PubKey
	proxyPort    int
	dmsgPort     uint16
	dmsgDisc     = dmsg.DiscAddr(false)
	useHTTP      bool
	httpClient   *http.Client
	dmsgSessions int
	dlog         *logging.Logger
	dmsgHTTPPath string
	err          error
)

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
func init() {
	RootCmd.AddCommand(
		serveCmd,
		proxyCmd,
	)

	serveCmd.Flags().Uint16VarP(&dmsgPort, "dport", "q", 1081, "dmsg port to serve socks5\033[0m\n\r")
	serveCmd.Flags().StringVarP(&wl, "wl", "w", "", "whitelist keys, comma separated\033[0m\n\r")
	serveCmd.Flags().StringVarP(&dmsgHTTPPath, "dmsgconf", "F", "", "dmsghttp-config path\033[0m\n\r")
	serveCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDisc, "dmsg discovery url\033[0m\n\r")
	serveCmd.Flags().BoolVarP(&useHTTP, "http", "z", false, "use regular http to connect to dmsg discovery\033[0m\n\r")
	if os.Getenv("DMSGSK") != "" {
		sk.Set(os.Getenv("DMSGSK")) //nolint
	}
	serveCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\033[0m\n\r")

	proxyCmd.Flags().IntVarP(&proxyPort, "port", "p", 1081, "TCP port to serve SOCKS5 proxy locally\033[0m\n\r")
	proxyCmd.Flags().Uint16VarP(&dmsgPort, "dport", "q", 1081, "dmsg port to connect to socks5 server\033[0m\n\r")
	proxyCmd.Flags().StringVarP(&pubk, "pk", "k", "", "dmsg socks5 proxy server public key to connect to\033[0m\n\r")
	proxyCmd.Flags().StringVarP(&dmsgHTTPPath, "dmsgconf", "F", "", "dmsghttp-config path\033[0m\n\r")
	proxyCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDisc, "dmsg discovery url\033[0m\n\r")
	proxyCmd.Flags().BoolVarP(&useHTTP, "http", "z", false, "use regular http to connect to dmsg discovery\033[0m\n\r")
	if os.Getenv("DMSGSK") != "" {
		sk.Set(os.Getenv("DMSGSK")) //nolint
	}
	proxyCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\033[0m\n\r")

}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG socks5 proxy server & client",
	Long: calvin.AsciiFont("dmsg-socks") + `
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
	Long:                  "dmsg socks5 proxy server",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		dlog = logging.MustGetLogger("dmsg-proxy")
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		go func() {
			<-interrupt
			dlog.Info("Interrupt received. Shutting down...")
			os.Exit(0)
		}()

		if dmsgHTTPPath != "" {
			dmsg.DmsghttpJSON, err = os.ReadFile(dmsgHTTPPath) //nolint
			if err != nil {
				dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
			}
			err = dmsg.InitConfig()
			if err != nil {
				dlog.WithError(err).Fatal("Failed to unmarshal dmsghttp-config")
			}
		}

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
				dlog.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
			} else {
				dlog.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
			}
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()
		//TODO: implement whitelist logic
		var dmsgC *dmsg.Client
		var closeDmsg func()

		if useHTTP {
			dlog.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).Debug("Connecting to dmsg network...")
			dmsgC, closeDmsg, err = cli.StartDmsg(ctx, dlog, pk, sk, httpClient, dmsgDisc, dmsgSessions)
		} else {
			dlog.WithField("public_key", pk.String()).Debug("Connecting to dmsg network...")
			dmsgC, closeDmsg, err = cli.StartDmsgDirect(ctx, dlog, pk, sk, httpClient, dmsgDisc, dmsgSessions, pk.String())
		}

		if err != nil {
			dlog.WithError(err).Fatal("Error connecting to dmsg network")
			return
		}

		defer closeDmsg()

		dlog.Infof("dmsg client pk: " + pk.String())
		time.Sleep(time.Second)
		dmsgL, err := dmsgC.Listen(dmsgPort)
		if err != nil {
			dlog.Fatalf("Error listening on port %d: %v", dmsgPort, err)
		}
		defer func() {
			if err := dmsgL.Close(); err != nil {
				dlog.Printf("Error closing listener: %v", err)
			}
		}()
		for {
			respConn, err := dmsgL.Accept()
			if err != nil {
				dlog.Errorf("Error accepting initiator: %v", err)
				continue
			}
			dlog.Infof("Accepted connection from: %s", respConn.RemoteAddr())

			conf := &socks5.Config{}
			server, err := socks5.New(conf)
			if err != nil {
				dlog.Fatalf("Error creating SOCKS5 server: %v", err)
			}
			go func() {
				defer func() {
					if closeErr := respConn.Close(); closeErr != nil {
						dlog.Printf("Error closing client connection: %v", closeErr)
					}
				}()
				if err := server.ServeConn(respConn); err != nil {
					dlog.Infof("Connection closed: %s", respConn.RemoteAddr())
					dlog.Errorf("Error serving SOCKS5 proxy: %v", err)
				}
			}()
		}
	},
}

// proxyCmd serves the local socks5 proxy
var proxyCmd = &cobra.Command{
	Use:                   "client",
	Short:                 "socks5 proxy client for dmsg socks5 proxy server",
	Long:                  "socks5 proxy client for dmsg socks5 proxy server",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		dlog = logging.MustGetLogger("dmsg-proxy-client")

		if dmsgHTTPPath != "" {
			dmsg.DmsghttpJSON, err = os.ReadFile(dmsgHTTPPath) //nolint
			if err != nil {
				dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
			}
			err = dmsg.InitConfig()
			if err != nil {
				dlog.WithError(err).Fatal("Failed to unmarshal dmsghttp-config")
			}
		}

		var pubKey cipher.PubKey
		err := pubKey.Set(pubk)
		if err != nil {
			dlog.Fatal("Public key to connect to cannot be empty")
		}
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		var dmsgC *dmsg.Client
		var closeDmsg func()

		if useHTTP {
			dlog.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).Debug("Connecting to dmsg network...")
			dmsgC, closeDmsg, err = cli.StartDmsg(ctx, dlog, pk, sk, httpClient, dmsgDisc, dmsgSessions)
		} else {
			dlog.WithField("public_key", pk.String()).Debug("Connecting to dmsg network...")
			dmsgC, closeDmsg, err = cli.StartDmsgDirect(ctx, dlog, pk, sk, httpClient, dmsgDisc, dmsgSessions, pk.String())
		}
		if err != nil {
			dlog.WithError(err).Fatal("Error connecting to dmsg network")
			return
		}

		defer closeDmsg()
		dmsgL, err := dmsgC.Listen(dmsgPort)
		if err != nil {
			dlog.Fatalf("Error listening by initiator on port %d: %v", dmsgPort, err)
		}
		defer func() {
			if err := dmsgL.Close(); err != nil {
				dlog.Printf("Error closing initiator's listener: %v", err)
			}
		}()
		dlog.Infof("Socks5 proxy client connected on DMSG port %d", dmsgPort)
		initTp, err := dmsgC.DialStream(context.Background(), dmsg.Addr{PK: pubKey, Port: dmsgPort})
		if err != nil {
			dlog.Fatalf("Error dialing responder: %v", err)
		}
		defer func() {
			if err := initTp.Close(); err != nil {
				dlog.Printf("Error closing initiator's stream: %v", err)
			}
		}()
		conf := &socks5.Config{}
		server, err := socks5.New(conf)
		if err != nil {
			dlog.Fatalf("Error creating SOCKS5 server: %v", err)
		}
		proxyListenAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
		dlog.Infof("Serving SOCKS5 proxy on %s", proxyListenAddr)
		if err := server.ListenAndServe("tcp", proxyListenAddr); err != nil {
			dlog.Fatalf("Error serving SOCKS5 proxy: %v", err)
		}
	},
}
