// Package commands cmd/network-monitor/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"

	"github.com/skycoin/skywire/pkg/network-monitor/api"
	"github.com/skycoin/skywire/pkg/network-monitor/store"
)

var (
	sdURL         string
	arURL         string
	utURL         string
	tpdURL        string
	dmsgdURL      string
	cleaningDelay int
	pk            string
	sk            string
	addr          string
	tag           string
	logLvl        string
	batchSize     int
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9080", "address to bind to.\033[0m")
	RootCmd.Flags().StringVar(&sdURL, "sd-url", "http://sd.skycoin.com", "url to service discovery\033[0m")
	RootCmd.Flags().StringVar(&arURL, "ar-url", "http://ar.skywire.skycoin.com", "url to address resolver\033[0m")
	RootCmd.Flags().StringVar(&utURL, "ut-url", "http://ut.skywire.skycoin.com", "url to uptime tracker visor data.\033[0m")
	RootCmd.Flags().StringVar(&tpdURL, "tpd-url", "http://tpd.skywire.skycoin.com", "url to transport discovery\033[0m")
	RootCmd.Flags().StringVar(&dmsgdURL, "dmsgd-url", "http://dmsgd.skywire.skycoin.com", "url to dmsg discovery\033[0m")
	RootCmd.Flags().IntVarP(&cleaningDelay, "cleaning-delay", "d", 75, "time for delay between each service cleaning routine")
	RootCmd.Flags().StringVar(&pk, "pk", "", "pk of network monitor\033[0m")
	RootCmd.Flags().StringVar(&sk, "sk", "", "sk of network monitor\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "network_monitor", "logging tag\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\033[0m")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Network monitor for skywire VPN and Visor.",
	Long: `
	┌┐┌┌─┐┌┬┐┬ ┬┌─┐┬─┐┬┌─   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	│││├┤  │ ││││ │├┬┘├┴┐───││││ │││││ │ │ │├┬┘
	┘└┘└─┘ ┴ └┴┘└─┘┴└─┴ ┴   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("failed to output build info: %v", err)
		}

		storeConfig := storeconfig.Config{
			Type: storeconfig.Memory,
		}

		s, err := store.New(storeConfig)
		if err != nil {
			log.Fatal("failed to initialize store: ", err)
		}

		mLogger := logging.NewMasterLogger()
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLogger.Fatal("invalid loglvl detected")
		}

		logging.SetLevel(lvl)

		var srvURLs api.ServicesURLs
		srvURLs.SD = sdURL
		srvURLs.AR = arURL
		srvURLs.UT = utURL
		srvURLs.DMSGD = dmsgdURL
		srvURLs.TPD = tpdURL

		logger := mLogger.PackageLogger("network_monitor")

		logger.WithField("addr", addr).Info("Serving Network Monitor API...")

		pubKey := cipher.PubKey{}
		pubKey.Set(pk) //nolint
		secKey := cipher.SecKey{}
		secKey.Set(sk) //nolint

		nmSign, _ := cipher.SignPayload([]byte(pubKey.Hex()), secKey) //nolint

		var nmConfig api.NetworkMonitorConfig
		nmConfig.CleaningDelay = cleaningDelay
		nmConfig.PublicKey = pubKey
		nmConfig.SecretKey = secKey
		nmConfig.Signature = nmSign

		nmAPI := api.New(s, logger, srvURLs, nmConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go nmAPI.InitCleaningLoop(ctx)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, nmAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("failed to execute command: ", err)
	}
}
