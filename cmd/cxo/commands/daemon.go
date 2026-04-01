package commands

import (
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cxo/node"
)

// Flag variables for CXO daemon — bound at init time to avoid calling
// node.NewConfig() during package init (which panics when $HOME is unset).
// These are applied to the real config in runDaemon.
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

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run CXO daemon",
	Long:  "CXO object distribution daemon. Listens for connections, replicates CX objects.",
	Run:   runDaemon,
}

func init() {
	f := daemonCmd.Flags()

	// node
	f.IntVar(&flagMaxConns, "max-connections", 0, "max connections, incoming and outgoing, tcp and udp")
	f.DurationVar(&flagMaxFillTime, "max-filling-time", 0, "max time to fill a Root")
	f.IntVar(&flagMaxHeads, "max-heads", 0, "max heads of a feed allowed")
	f.StringVar(&flagRPC, "rpc", "", "RPC listening address")

	// TCP
	f.StringVar(&flagTCPListen, "tcp", "", "TCP listening address")
	f.DurationVar(&flagTCPRespTO, "tcp-response-timeout", 0, "response timeout of TCP connections")
	f.DurationVar(&flagTCPPings, "tcp-pings", 0, "pings interval of TCP connections")

	// UDP
	f.StringVar(&flagUDPListen, "udp", "", "UDP listening address")
	f.DurationVar(&flagUDPRespTO, "udp-response-timeout", 0, "response timeout of UDP connections")
	f.DurationVar(&flagUDPPings, "udp-pings", 0, "pings interval of UDP connections")

	// public
	f.BoolVar(&flagPublic, "public", false, "public server")

	// logger
	f.StringVar(&flagLogPrefix, "log-prefix", "", "log prefix")
	f.BoolVar(&flagDebug, "debug", false, "print debug logs")

	// skyobject
	f.BoolVar(&flagMemDB, "mem-db", false, "use in-memory database")
	f.StringVar(&flagDataDir, "data-dir", "", "data directory")
}

func runDaemon(cmd *cobra.Command, _ []string) {
	// Create config with real defaults here (not at init time).
	cfg := node.NewConfig()
	cfg.OnSubscribeRemote = acceptAllSubscriptions

	// Override defaults with any explicitly-set flags.
	if cmd.Flags().Changed("max-connections") {
		cfg.MaxConnections = flagMaxConns
	}
	if cmd.Flags().Changed("max-filling-time") {
		cfg.MaxFillingTime = flagMaxFillTime
	}
	if cmd.Flags().Changed("max-heads") {
		cfg.MaxHeads = flagMaxHeads
	}
	if cmd.Flags().Changed("rpc") {
		cfg.RPC = flagRPC
	}
	if cmd.Flags().Changed("tcp") {
		cfg.TCP.Listen = flagTCPListen
	}
	if cmd.Flags().Changed("tcp-response-timeout") {
		cfg.TCP.ResponseTimeout = flagTCPRespTO
	}
	if cmd.Flags().Changed("tcp-pings") {
		cfg.TCP.Pings = flagTCPPings
	}
	if cmd.Flags().Changed("udp") {
		cfg.UDP.Listen = flagUDPListen
	}
	if cmd.Flags().Changed("udp-response-timeout") {
		cfg.UDP.ResponseTimeout = flagUDPRespTO
	}
	if cmd.Flags().Changed("udp-pings") {
		cfg.UDP.Pings = flagUDPPings
	}
	if cmd.Flags().Changed("public") {
		cfg.Public = flagPublic
	}
	if cmd.Flags().Changed("log-prefix") {
		cfg.Logger.Prefix = flagLogPrefix
	}
	if cmd.Flags().Changed("debug") {
		cfg.Logger.Debug = flagDebug
	}
	if cmd.Flags().Changed("mem-db") {
		cfg.Config.InMemoryDB = flagMemDB
	}
	if cmd.Flags().Changed("data-dir") {
		cfg.Config.DataDir = flagDataDir
	}

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
