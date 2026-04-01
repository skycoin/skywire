package commands

import (
	"log"
	"os"
	"os/signal"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cxo/node"
)

var cfg *node.Config

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run CXO daemon",
	Long:  "CXO object distribution daemon. Listens for connections, replicates CX objects.",
	PreRun: func(_ *cobra.Command, _ []string) {
		cfg = node.NewConfig()
		cfg.OnSubscribeRemote = acceptAllSubscriptions
	},
	Run: runDaemon,
}

func init() {
	// Use zero-value defaults for flag registration; actual defaults
	// are applied in PreRun via node.NewConfig(). This avoids calling
	// NewConfig() at package init time, which panics when $HOME is unset
	// (e.g. systemd services).
	defaults := node.Config{}

	f := daemonCmd.Flags()

	// node
	f.IntVar(&defaults.MaxConnections, "max-connections", 0,
		"max connections, incoming and outgoing, tcp and udp")
	f.DurationVar(&defaults.MaxFillingTime, "max-filling-time", 0,
		"max time to fill a Root")
	f.IntVar(&defaults.MaxHeads, "max-heads", 0,
		"max heads of a feed allowed")
	f.StringVar(&defaults.RPC, "rpc", "",
		"RPC listening address")

	// TCP
	f.StringVar(&defaults.TCP.Listen, "tcp", "",
		"TCP listening address")
	f.DurationVar(&defaults.TCP.ResponseTimeout, "tcp-response-timeout", 0,
		"response timeout of TCP connections")
	f.DurationVar(&defaults.TCP.Pings, "tcp-pings", 0,
		"pings interval of TCP connections")

	// UDP
	f.StringVar(&defaults.UDP.Listen, "udp", "",
		"UDP listening address")
	f.DurationVar(&defaults.UDP.ResponseTimeout, "udp-response-timeout", 0,
		"response timeout of UDP connections")
	f.DurationVar(&defaults.UDP.Pings, "udp-pings", 0,
		"pings interval of UDP connections")

	// public
	f.BoolVar(&defaults.Public, "public", false,
		"public server")

	// logger
	f.StringVar(&defaults.Logger.Prefix, "log-prefix", "",
		"log prefix")
	f.BoolVar(&defaults.Logger.Debug, "debug", false,
		"print debug logs")

	// skyobject
	f.BoolVar(&defaults.Config.InMemoryDB, "mem-db", false,
		"use in-memory database")
	f.StringVar(&defaults.Config.DataDir, "data-dir", "",
		"data directory")
}

func runDaemon(cmd *cobra.Command, args []string) {
	// Apply any flag overrides to the config created in PreRun
	cmd.Flags().Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "max-connections":
			cfg.MaxConnections, _ = cmd.Flags().GetInt("max-connections")
		case "max-filling-time":
			cfg.MaxFillingTime, _ = cmd.Flags().GetDuration("max-filling-time")
		case "max-heads":
			cfg.MaxHeads, _ = cmd.Flags().GetInt("max-heads")
		case "rpc":
			cfg.RPC, _ = cmd.Flags().GetString("rpc")
		case "tcp":
			cfg.TCP.Listen, _ = cmd.Flags().GetString("tcp")
		case "tcp-response-timeout":
			cfg.TCP.ResponseTimeout, _ = cmd.Flags().GetDuration("tcp-response-timeout")
		case "tcp-pings":
			cfg.TCP.Pings, _ = cmd.Flags().GetDuration("tcp-pings")
		case "udp":
			cfg.UDP.Listen, _ = cmd.Flags().GetString("udp")
		case "udp-response-timeout":
			cfg.UDP.ResponseTimeout, _ = cmd.Flags().GetDuration("udp-response-timeout")
		case "udp-pings":
			cfg.UDP.Pings, _ = cmd.Flags().GetDuration("udp-pings")
		case "public":
			cfg.Public, _ = cmd.Flags().GetBool("public")
		case "log-prefix":
			cfg.Logger.Prefix, _ = cmd.Flags().GetString("log-prefix")
		case "debug":
			cfg.Logger.Debug, _ = cmd.Flags().GetBool("debug")
		case "mem-db":
			cfg.Config.InMemoryDB, _ = cmd.Flags().GetBool("mem-db")
		case "data-dir":
			cfg.Config.DataDir, _ = cmd.Flags().GetString("data-dir")
		}
	})

	n, err := node.NewNode(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer n.Close() //nolint:errcheck,gosec

	waitInterrupt()
}

func waitInterrupt() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}

func acceptAllSubscriptions(c *node.Conn, pk cipher.PubKey) (_ error) {
	if err := c.Node().Share(pk); err != nil {
		log.Fatal("DB failure:", err)
	}
	return
}
