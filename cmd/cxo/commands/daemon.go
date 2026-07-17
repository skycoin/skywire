// Package commands cmd/cxo/commands/daemon.go c2-net-cxo
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

// skHex is the optional node identity secret key (hex). Empty = the node
// generates a random ephemeral identity each start (the historical default),
// which means its public key — and therefore the `<pk>@host:port` address other
// nodes/utilities use to reach it — changes on every restart. Provide --sk to
// pin a stable identity, matching the pk-as-identity convention the treestore-
// backed CXO utilities (e.g. skychat) already use.
var skHex string

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run CXO daemon",
	Long:  "CXO object distribution daemon. Listens for connections, replicates CX objects.",
	Run:   runDaemon,
}

func init() {
	cfg.OnSubscribeRemote = acceptAllSubscriptions

	f := daemonCmd.Flags()

	// identity
	f.StringVar(&skHex, "sk", "",
		"node identity secret key (hex); empty = random ephemeral identity (PK changes each restart)")

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

func runDaemon(_ *cobra.Command, _ []string) {
	if skHex != "" {
		sk, err := cipher.SecKeyFromHex(skHex)
		if err != nil {
			log.Fatalf("invalid --sk: %v", err)
		}
		cfg.SecKey = sk
	}

	n, err := node.NewNode(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer n.Close() //nolint:errcheck,gosec

	// Surface the node's identity + how to reach it, so operators can use the
	// pk-as-identity (<pk>@host:port) convention. Without --sk the PK below is
	// random and changes on the next restart.
	pk := n.ID()
	log.Printf("CXO node identity (pk): %s", pk.Hex())
	if skHex == "" {
		log.Printf("CXO: ephemeral identity (random) — pass --sk <hex> to pin a stable pk across restarts")
	}
	if cfg.TCP.Listen != "" {
		log.Printf("CXO reachable (tcp): %s@<host>%s", pk.Hex(), cfg.TCP.Listen)
	}
	if cfg.UDP.Listen != "" {
		log.Printf("CXO reachable (udp): %s@<host>%s", pk.Hex(), cfg.UDP.Listen)
	}

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
