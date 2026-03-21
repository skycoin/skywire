package commands

import (
	"log"
	"os"
	"os/signal"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cxo/node"
)

var cfg = node.NewConfig()

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run CXO daemon",
	Long:  "CXO object distribution daemon. Listens for connections, replicates CX objects.",
	Run:   runDaemon,
}

func init() {
	cfg.OnSubscribeRemote = acceptAllSubscriptions

	f := daemonCmd.Flags()

	// node
	f.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections,
		"max connections, incoming and outgoing, tcp and udp")
	f.DurationVar(&cfg.MaxFillingTime, "max-filling-time", cfg.MaxFillingTime,
		"max time to fill a Root")
	f.IntVar(&cfg.MaxHeads, "max-heads", cfg.MaxHeads,
		"max heads of a feed allowed")
	f.StringVar(&cfg.RPC, "rpc", cfg.RPC,
		"RPC listening address")

	// TCP
	f.StringVar(&cfg.TCP.Listen, "tcp", cfg.TCP.Listen,
		"TCP listening address")
	f.DurationVar(&cfg.TCP.ResponseTimeout, "tcp-response-timeout", cfg.TCP.ResponseTimeout,
		"response timeout of TCP connections")
	f.DurationVar(&cfg.TCP.Pings, "tcp-pings", cfg.TCP.Pings,
		"pings interval of TCP connections")

	// UDP
	f.StringVar(&cfg.UDP.Listen, "udp", cfg.UDP.Listen,
		"UDP listening address")
	f.DurationVar(&cfg.UDP.ResponseTimeout, "udp-response-timeout", cfg.UDP.ResponseTimeout,
		"response timeout of UDP connections")
	f.DurationVar(&cfg.UDP.Pings, "udp-pings", cfg.UDP.Pings,
		"pings interval of UDP connections")

	// public
	f.BoolVar(&cfg.Public, "public", cfg.Public,
		"public server")

	// logger
	f.StringVar(&cfg.Logger.Prefix, "log-prefix", cfg.Logger.Prefix,
		"log prefix")
	f.BoolVar(&cfg.Logger.Debug, "debug", cfg.Logger.Debug,
		"print debug logs")

	// skyobject
	f.BoolVar(&cfg.Config.InMemoryDB, "mem-db", cfg.Config.InMemoryDB,
		"use in-memory database")
	f.StringVar(&cfg.Config.DataDir, "data-dir", cfg.Config.DataDir,
		"data directory")
}

func runDaemon(cmd *cobra.Command, args []string) {
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
