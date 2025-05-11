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
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	"github.com/skycoin/dmsg/internal/cli"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	dmsgDisc     = dmsg.DiscAddr(false)
	sk           cipher.SecKey
	logLvl       string
	dmsgServers  []string
	proxyAddr    string
	httpClient   *http.Client
	useHTTP      bool
	dmsgSessions int
	dmsgHTTPPath string
	err          error
)

func init() {
	RootCmd.Flags().BoolVarP(&useHTTP, "http", "z", false, "use regular http to connect to dmsg discovery")
	RootCmd.Flags().StringVarP(&dmsgHTTPPath, "dmsgconf", "F", "", "dmsghttp-config path")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "c", dmsgDisc, "dmsg discovery url\033[0m\n\r")
	RootCmd.Flags().IntVarP(&dmsgSessions, "sess", "e", 1, "number of dmsg servers to connect to\033[0m\n\r")
	RootCmd.Flags().StringVarP(&proxyAddr, "proxy", "p", "", "connect to dmsg via proxy (i.e. '127.0.0.1:1080')")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	if os.Getenv("DMSGIP_SK") != "" {
		sk.Set(os.Getenv("DMSGIP_SK")) //nolint
	}
	RootCmd.Flags().StringSliceVarP(&dmsgServers, "srv", "d", []string{}, "dmsg server public keys")
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r\033[0m")
}

// RootCmd contains the root dmsgcurl command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG IP utility",
	Long: calvin.AsciiFont("dmsgip") + `
	DMSG IP utility`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	RunE: func(_ *cobra.Command, _ []string) error {
		dlog := logging.MustGetLogger("dmsgip")

		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

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

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		httpClient = &http.Client{}
		var dialer proxy.Dialer = proxy.Direct

		if proxyAddr != "" {
			dialer, err = proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				dlog.Fatalf("Error creating SOCKS5 dialer: %v", err)
			}
			transport := &http.Transport{
				Dial: dialer.Dial,
			}
			httpClient = &http.Client{
				Transport: transport,
			}
			ctx = context.WithValue(context.Background(), "socks5_proxy", proxyAddr) //nolint
		}

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
			dlog.WithError(err).Debug("Error connecting to dmsg network")
		}
		defer closeDmsg()
		ip, err := dmsgC.LookupIP(ctx, srvs)
		if err != nil {
			dlog.WithError(err).Error("failed to lookup IP")
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
