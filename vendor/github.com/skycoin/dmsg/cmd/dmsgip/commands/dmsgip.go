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

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	dmsgDisc    string
	sk          cipher.SecKey
	logLvl      string
	dmsgServers []string
)

func init() {
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "c", "", "dmsg discovery url default:\n"+skyenv.DmsgDiscAddr)
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m")
	if os.Getenv("DMSGIP_SK") != "" {
		sk.Set(os.Getenv("DMSGIP_SK")) //nolint
	}
	RootCmd.Flags().StringSliceVarP(&dmsgServers, "srv", "d", []string{}, "dmsg server public keys\n\r")
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
}

// RootCmd containsa the root dmsgcurl command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG ip utility",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐ ┬┌─┐
	 │││││└─┐│ ┬ │├─┘
	─┴┘┴ ┴└─┘└─┘ ┴┴
DMSG ip utility`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	PreRun: func(cmd *cobra.Command, args []string) {
		if dmsgDisc == "" {
			dmsgDisc = skyenv.DmsgDiscAddr
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
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

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		dmsgC, closeDmsg, err := startDmsg(ctx, log, pk, sk)
		if err != nil {
			log.WithError(err).Error("failed to start dmsg")
		}
		defer closeDmsg()

		ip, err := dmsgC.LookupIP(ctx, srvs)
		if err != nil {
			log.WithError(err).Error("failed to lookup IP")
		}

		fmt.Printf("%v\n", ip)
		fmt.Print("\n")
		return nil
	},
}

func startDmsg(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), &dmsg.Config{MinSessions: dmsg.DefaultMinSessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		log.WithError(err).Debug("Disconnected from dmsg network.")
		fmt.Printf("\n")
	}
	log.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).
		Debug("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		log.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
