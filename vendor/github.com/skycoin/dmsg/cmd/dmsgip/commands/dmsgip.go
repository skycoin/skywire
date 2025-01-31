// Package commands cmd/dmsgcurl/commands/dmsgcurl.go
package commands

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	dmsgDisc    = dmsg.DiscAddr(false)
	sk          cipher.SecKey
	logLvl      string
	dmsgServers []string
	proxyAddr   string
	httpClient  *http.Client
)

func init() {
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "c", dmsgDisc, "dmsg discovery url\033[0m")
	RootCmd.Flags().StringVarP(&proxyAddr, "proxy", "p", "", "connect to dmsg via proxy (i.e. '127.0.0.1:1080')")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m")
	if os.Getenv("DMSGIP_SK") != "" {
		sk.Set(os.Getenv("DMSGIP_SK")) //nolint
	}
	RootCmd.Flags().StringSliceVarP(&dmsgServers, "srv", "d", []string{}, "dmsg server public keys\n\r\033[0m")
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r\033[0m")
}

// RootCmd contains the root dmsgcurl command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG IP utility",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐ ┬┌─┐
	 │││││└─┐│ ┬ │├─┘
	─┴┘┴ ┴└─┘└─┘ ┴┴
	DMSG IP utility`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	RunE: func(_ *cobra.Command, _ []string) error {
		log := logging.MustGetLogger("dmsgip")

		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		var srvs []cipher.PubKey
		for _, srv := range dmsgServers {
			var pk cipher.PubKey
			if err := pk.Set(srv); err != nil {
				return fmt.Errorf("failed to parse server public key: %w", err)
			}
			srvs = append(srvs, pk)
		}

		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		httpClient = &http.Client{}
		var dialer proxy.Dialer = proxy.Direct // Default dialer is direct connection

		if proxyAddr != "" {
			// Use SOCKS5 proxy dialer if specified
			dialer, err = proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				log.Fatalf("Error creating SOCKS5 dialer: %v", err)
			}
			transport := &http.Transport{
				Dial: dialer.Dial,
			}
			httpClient = &http.Client{
				Transport: transport,
			}
			ctx = context.WithValue(context.Background(), "socks5_proxy", proxyAddr) //nolint
		}

		// Create DMSG client
		dmsgC := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, httpClient, log), &dmsg.Config{MinSessions: dmsg.DefaultMinSessions})
		go dmsgC.Serve(ctx) // Pass the context here

		stop := func() {
			err := dmsgC.Close()
			log.WithError(err).Debug("Disconnected from dmsg network.")
			fmt.Printf("\n")
		}
		defer stop()

		log.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).
			Debug("Connecting to dmsg network...")

		select {
		case <-ctx.Done():
			stop()
			return ctx.Err()

		case <-dmsgC.Ready():
			log.Debug("Dmsg network ready.")
		}

		// Perform IP lookup using the context with the proxy dialer
		ip, err := dmsgC.LookupIP(ctx, srvs)
		if err != nil {
			log.WithError(err).Error("failed to lookup IP")
		}

		fmt.Printf("%v\n", ip)
		fmt.Print("\n")
		return nil
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
