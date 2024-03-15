// Package commands cmd/config-bootstrapper/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/config-bootstrapper/api"
)

var (
	addr     string
	tag      string
	stunPath string
	domain   string
	dmsgDisc string
	sk       cipher.SecKey
	dmsgPort uint16
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9082", "address to bind to\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "address_resolver", "logging tag\033[0m")
	RootCmd.Flags().StringVarP(&stunPath, "config", "c", "./config.json", "stun server list file location\033[0m")
	RootCmd.Flags().StringVarP(&domain, "domain", "d", "skywire.skycoin.com", "the domain of the endpoints\033[0m")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", "http://dmsgd.skywire.skycoin.com", "url of dmsg-discovery\033[0m")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\r")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Config Bootstrap Server for skywire",
	Long: `
	┌─┐┌─┐┌┐┌┌─┐┬┌─┐   ┌┐ ┌─┐┌─┐┌┬┐┌─┐┌┬┐┬─┐┌─┐┌─┐┌─┐┌─┐┬─┐
	│  │ ││││├┤ ││ ┬───├┴┐│ ││ │ │ └─┐ │ ├┬┘├─┤├─┘├─┘├┤ ├┬┘
	└─┘└─┘┘└┘└  ┴└─┘   └─┘└─┘└─┘ ┴ └─┘ ┴ ┴└─┴ ┴┴  ┴  └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		logger := logging.MustGetLogger(tag)
		config := readConfig(logger, stunPath)

		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		conAPI := api.New(logger, config, domain, dmsgAddr)
		if logger != nil {
			logger.Infof("Listening on %s", addr)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, logger)

			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), logger)
			config := &dmsg.Config{
				MinSessions:    0, // listen on all available servers
				UpdateInterval: dmsg.DefaultUpdateInterval,
			}

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
			if err != nil {
				logger.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, logger)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, conAPI, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, logger); err != nil {
					logger.Errorf("dmsghttp.ListenAndServe: %v", err)
					cancel()
				}
			}()
		}

		go func() {
			if err := tcpproxy.ListenAndServe(addr, conAPI); err != nil {
				logger.Errorf("conAPI.ListenAndServe: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()

		conAPI.Close()
	},
}

func readConfig(log *logging.Logger, confPath string) (config api.Config) {
	var r io.Reader

	f, err := os.Open(confPath) //nolint:gosec
	if err != nil {
		log.WithError(err).
			WithField("filepath", confPath).
			Fatal("Failed to read config file.")
	}
	defer func() { //nolint
		if err := f.Close(); err != nil {
			log.WithError(err).Fatal("Closing config file resulted in error.")
		}
	}()

	r = f

	raw, err := io.ReadAll(r)
	if err != nil {
		log.WithError(err).Fatal("Failed to read in config.")
	}
	conf := api.Config{}

	if err := json.Unmarshal(raw, &conf); err != nil {
		log.WithError(err).Fatal("failed to convert config into json.")
	}

	return conf
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
