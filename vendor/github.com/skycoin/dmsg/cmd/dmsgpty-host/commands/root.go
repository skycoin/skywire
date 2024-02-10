// Package commands cmd/dmsgpty-host/commands/root.go
package commands

import (
	"context"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
)

const defaultEnvPrefix = "DMSGPTY"

var log = logging.MustGetLogger("dmsgpty-host:init")

var json = jsoniter.ConfigFastest

// variables
var (
	// persistent flags
	dmsgDisc     = dmsg.DefaultDiscAddr
	dmsgSessions = dmsg.DefaultMinSessions
	dmsgPort     = dmsgpty.DefaultPort
	cliNet       = dmsgpty.DefaultCLINet
	cliAddr      = dmsgpty.DefaultCLIAddr()
	sk           cipher.SecKey
	pk           cipher.PubKey
	wl           cipher.PubKeys

	// persistent flags
	envPrefix = defaultEnvPrefix

	// root command flags
	confStdin = false
	confPath  = "./config.json"
)

// init prepares flags.
func init() {

	// Prepare flags with env/config references.
	RootCmd.Flags().Var(&wl, "wl", "whitelist of the dmsgpty-host")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsgdisc", dmsgDisc, "dmsg discovery address")
	RootCmd.Flags().IntVar(&dmsgSessions, "dmsgsessions", dmsgSessions, "minimum number of dmsg sessions to ensure")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgport", dmsgPort, "dmsg port for listening for remote hosts")
	RootCmd.Flags().StringVar(&cliNet, "clinet", cliNet, "network used for listening for cli connections")
	RootCmd.Flags().StringVar(&cliAddr, "cliaddr", cliAddr, "address used for listening for cli connections")
	// Prepare flags without associated env/config references.
	RootCmd.Flags().StringVar(&envPrefix, "envprefix", envPrefix, "env prefix")
	RootCmd.Flags().BoolVar(&confStdin, "confstdin", confStdin, "config will be read from stdin if set")
	RootCmd.Flags().StringVarP(&confPath, "confpath", "c", confPath, "config path")

}

// RootCmd contains commands for dmsgpty-host
var RootCmd = &cobra.Command{
	Use:   "host",
	Short: "DMSG host for pseudoterminal command line interface",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┌─┐┌─┐┌┬┐
	 │││││└─┐│ ┬├─┘ │ └┬┘───├─┤│ │└─┐ │
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    ┴ ┴└─┘└─┘ ┴
  ` + "DMSG host for pseudoterminal command line interface",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	PreRun:                func(cmd *cobra.Command, args []string) {},
	RunE: func(cmd *cobra.Command, args []string) error {
		conf, err := getConfig(cmd, false)
		if err != nil {
			return fmt.Errorf("failed to get config: %w", err)
		}

		if _, err := buildinfo.Get().WriteTo(stdlog.Writer()); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		log := logging.MustGetLogger("dmsgpty-host")
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		pk, err := sk.PubKey()
		if err != nil {
			return fmt.Errorf("failed to derive public key from secret key: %w", err)
		}

		// Prepare and serve dmsg client and wait until ready.
		dmsgC := dmsg.NewClient(pk, sk, disc.NewHTTP(conf.DmsgDisc, &http.Client{}, log), &dmsg.Config{
			MinSessions: conf.DmsgSessions,
		})
		go dmsgC.Serve(context.Background())
		select {
		case <-ctx.Done():
			return fmt.Errorf("failed to wait dmsg client to be ready: %w", ctx.Err())
		case <-dmsgC.Ready():
		}

		// Prepare whitelist.
		// var wl dmsgpty.Whitelist
		wl, err := dmsgpty.NewConfigWhitelist(confPath)
		if err != nil {
			return fmt.Errorf("failed to init whitelist: %w", err)
		}

		// Prepare dmsgpty host.
		host := dmsgpty.NewHost(dmsgC, wl)
		wg := new(sync.WaitGroup)
		wg.Add(2)

		// Prepare CLI.
		if conf.CLINet == "unix" {
			_ = os.Remove(conf.CLIAddr) //nolint:errcheck
		}
		cliL, err := net.Listen(conf.CLINet, conf.CLIAddr)
		if err != nil {
			return fmt.Errorf("failed to serve CLI: %w", err)
		}
		log.WithField("addr", cliL.Addr()).Info("Listening for CLI connections.")
		go func() {
			log.WithError(host.ServeCLI(ctx, cliL)).
				Info("Stopped serving CLI.")
			wg.Done()
		}()

		// Serve dmsgpty.
		log.WithField("port", conf.DmsgPort).
			Info("Listening for dmsg streams.")
		go func() {
			log.WithError(host.ListenAndServe(ctx, conf.DmsgPort)).
				Info("Stopped serving dmsgpty-host.")
			wg.Done()
		}()

		wg.Wait()
		return nil
	},
}

// Execute executes the root command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func configFromJSON(conf dmsgpty.Config) (dmsgpty.Config, error) {
	var jsonConf dmsgpty.Config

	if confStdin {
		if err := json.NewDecoder(os.Stdin).Decode(&jsonConf); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("flag 'confstdin' is set, but config read from stdin is invalid: %w", err)
		}
	}

	if confPath != "" {
		f, err := os.Open(confPath)
		if err != nil {
			return dmsgpty.Config{}, fmt.Errorf("failed to open config file: %w", err)
		}
		if err := json.NewDecoder(f).Decode(&jsonConf); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("flag 'confpath' is set, but we failed to read config from specified path: %w", err)
		}
	}

	if jsonConf.SK != "" {
		if err := sk.Set(jsonConf.SK); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("provided SK is invalid: %w", err)
		}
	}

	if !sk.Null() {
		conf.SK = jsonConf.SK
	}

	if jsonConf.PK != "" {
		if err := pk.Set(jsonConf.PK); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("provided PK is invalid: %w", err)
		}
	}

	if !pk.Null() {
		conf.PK = jsonConf.PK
	}

	if len(jsonConf.WL) > 0 {
		ustString := strings.Join(jsonConf.WL, ",")
		if err := wl.Set(ustString); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("provided WL's are invalid: %w", err)
		}
	}

	if len(wl) > 0 {
		conf.WL = jsonConf.WL
	}

	if jsonConf.DmsgDisc != "" {
		conf.DmsgDisc = jsonConf.DmsgDisc
	}

	if conf.DmsgSessions != 0 {
		conf.DmsgSessions = jsonConf.DmsgSessions
	}

	if conf.DmsgPort != 0 {
		conf.DmsgPort = jsonConf.DmsgPort
	}

	if conf.CLINet != "" {
		conf.CLINet = jsonConf.CLINet
	}

	if conf.CLIAddr != "" {
		conf.CLIAddr = dmsgpty.ParseWindowsEnv(jsonConf.CLIAddr)
	}

	if conf.CLIAddr == "" {
		conf.CLIAddr = dmsgpty.DefaultCLIAddr()
	}

	return conf, nil
}

func fillConfigFromENV(conf dmsgpty.Config) (dmsgpty.Config, error) {

	if val, ok := os.LookupEnv(envPrefix + "_DMSGDISC"); ok {
		conf.DmsgDisc = val
	}

	if val, ok := os.LookupEnv(envPrefix + "_DMSGSESSIONS"); ok {
		dmsgSessions, err := strconv.Atoi(val)
		if err != nil {
			return conf, fmt.Errorf("failed to parse dmsg sessions: %w", err)
		}

		conf.DmsgSessions = dmsgSessions
	}

	if val, ok := os.LookupEnv(envPrefix + "_DMSGPORT"); ok {
		dmsgPort, err := strconv.ParseUint(val, 10, 16)
		if err != nil {
			return conf, fmt.Errorf("failed to parse dmsg port: %w", err)
		}

		conf.DmsgPort = uint16(dmsgPort)
	}

	if val, ok := os.LookupEnv(envPrefix + "_CLINET"); ok {
		conf.CLINet = val
	}

	if val, ok := os.LookupEnv(envPrefix + "_CLIADDR"); ok {
		conf.CLIAddr = val
	}

	return conf, nil
}

func fillConfigFromFlags(conf dmsgpty.Config) dmsgpty.Config {
	if dmsgDisc != dmsg.DefaultDiscAddr {
		conf.DmsgDisc = dmsgDisc
	}

	if dmsgSessions != dmsg.DefaultMinSessions {
		conf.DmsgSessions = dmsgSessions
	}

	if dmsgPort != dmsgpty.DefaultPort {
		conf.DmsgPort = dmsgPort
	}

	if cliNet != dmsgpty.DefaultCLINet {
		conf.CLINet = cliNet
	}

	if cliAddr != dmsgpty.DefaultCLIAddr() {
		conf.CLIAddr = cliAddr
	}

	return conf
}

// getConfig sources variables in the following precedence order: flags, env, config, default.
func getConfig(cmd *cobra.Command, skGen bool) (dmsgpty.Config, error) {
	conf := dmsgpty.DefaultConfig()

	var err error
	// Prepare how config file is sourced (if root command).
	if cmd.Name() == cmdutil.RootCmdName() {
		conf, err = configFromJSON(conf)
		if err != nil {
			return dmsgpty.Config{}, fmt.Errorf("failed to read config from JSON: %w", err)
		}
	}
	conf, err = fillConfigFromENV(conf)
	if err != nil {
		return conf, fmt.Errorf("failed to fill config from ENV: %w", err)
	}

	if skGen {
		pk, sk = cipher.GenerateKeyPair()
		log.WithField("pubkey", pk).
			WithField("seckey", sk).
			Info("Generating key pair as 'skgen' is set.")
		conf.SK = sk.Hex()
		conf.PK = pk.Hex()
	}
	conf = fillConfigFromFlags(conf)

	if sk.Null() {
		return conf, fmt.Errorf("value 'seckey' is invalid")
	}

	// Print values.
	pLog := logrus.FieldLogger(log)
	pLog = pLog.WithField("dmsgdisc", conf.DmsgDisc)
	pLog = pLog.WithField("dmsgsessions", conf.DmsgSessions)
	pLog = pLog.WithField("dmsgport", conf.DmsgPort)
	pLog = pLog.WithField("clinet", conf.CLINet)
	pLog = pLog.WithField("cliaddr", conf.CLIAddr)
	pLog = pLog.WithField("pk", conf.PK)
	pLog = pLog.WithField("wl", conf.WL)
	pLog.Info("Init complete.")

	return conf, nil
}
