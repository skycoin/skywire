package commands

import (
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cxo/node"
)

// Flag variables for CXO daemon — bound at init, applied to cfg in PreRun.
var (
	flagMaxConns    int
	flagMaxFillTime time.Duration
	flagMaxHeads    int
	flagRPC         string
	flagTCPListen   string
	flagTCPRespTO   time.Duration
	flagTCPPings    time.Duration
	flagUDPListen   string
	flagUDPRespTO   time.Duration
	flagUDPPings    time.Duration
	flagPublic      bool
	flagLogPrefix   string
	flagDebug       bool
	flagMemDB       bool
	flagDataDir     string
)

var cfg *node.Config

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run CXO daemon",
	Long:  "CXO object distribution daemon. Listens for connections, replicates CX objects.",
	PreRun: func(cmd *cobra.Command, _ []string) {
		// Create config with real defaults (requires $HOME for DataDir).
		// This runs only when the cxo daemon command is actually invoked.
		cfg = node.NewConfig()
		cfg.OnSubscribeRemote = acceptAllSubscriptions

		// Apply only flags that were explicitly set by the user.
		cmd.Flags().Visit(func(f *pflag.Flag) { //nolint:errcheck,gosec
			switch f.Name {
			case "max-connections":
				cfg.MaxConnections = flagMaxConns
			case "max-filling-time":
				cfg.MaxFillingTime = flagMaxFillTime
			case "max-heads":
				cfg.MaxHeads = flagMaxHeads
			case "rpc":
				cfg.RPC = flagRPC
			case "tcp":
				cfg.TCP.Listen = flagTCPListen
			case "tcp-response-timeout":
				cfg.TCP.ResponseTimeout = flagTCPRespTO
			case "tcp-pings":
				cfg.TCP.Pings = flagTCPPings
			case "udp":
				cfg.UDP.Listen = flagUDPListen
			case "udp-response-timeout":
				cfg.UDP.ResponseTimeout = flagUDPRespTO
			case "udp-pings":
				cfg.UDP.Pings = flagUDPPings
			case "public":
				cfg.Public = flagPublic
			case "log-prefix":
				cfg.Logger.Prefix = flagLogPrefix
			case "debug":
				cfg.Logger.Debug = flagDebug
			case "mem-db":
				cfg.Config.InMemoryDB = flagMemDB
			case "data-dir":
				cfg.Config.DataDir = flagDataDir
			}
		})
	},
	Run: runDaemon,
}

func init() {
	f := daemonCmd.Flags()

	f.IntVar(&flagMaxConns, "max-connections", 0, "max connections, incoming and outgoing, tcp and udp")
	f.DurationVar(&flagMaxFillTime, "max-filling-time", 0, "max time to fill a Root")
	f.IntVar(&flagMaxHeads, "max-heads", 0, "max heads of a feed allowed")
	f.StringVar(&flagRPC, "rpc", "", "RPC listening address")

	f.StringVar(&flagTCPListen, "tcp", "", "TCP listening address")
	f.DurationVar(&flagTCPRespTO, "tcp-response-timeout", 0, "response timeout of TCP connections")
	f.DurationVar(&flagTCPPings, "tcp-pings", 0, "pings interval of TCP connections")

	f.StringVar(&flagUDPListen, "udp", "", "UDP listening address")
	f.DurationVar(&flagUDPRespTO, "udp-response-timeout", 0, "response timeout of UDP connections")
	f.DurationVar(&flagUDPPings, "udp-pings", 0, "pings interval of UDP connections")

	f.BoolVar(&flagPublic, "public", false, "public server")

	f.StringVar(&flagLogPrefix, "log-prefix", "", "log prefix")
	f.BoolVar(&flagDebug, "debug", false, "print debug logs")

	f.BoolVar(&flagMemDB, "mem-db", false, "use in-memory database")
	f.StringVar(&flagDataDir, "data-dir", "", "data directory")
}

func runDaemon(_ *cobra.Command, _ []string) {
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
