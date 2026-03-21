package commands

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/peterh/liner"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

const (
	historyFile = ".cxocli.history"
	defaultAddr = "[::]:8871"
)

var (
	out io.Writer = os.Stdout

	rpcAddress string
	execCmd    string
)

var cliCmd = &cobra.Command{
	Use:   "cli",
	Short: "CXO command line interface",
	Long:  "CXO CLI client for interacting with a running CXO daemon via RPC.",
	Run:   runInteractive,
}

func init() {
	cliCmd.Flags().StringVarP(&rpcAddress, "address", "a", defaultAddr,
		"RPC address to connect to")
	cliCmd.Flags().StringVarP(&execCmd, "exec", "e", "",
		"execute command and exit")

	// Subcommands
	cliCmd.AddCommand(feedCmd)
	cliCmd.AddCommand(tcpCmd)
	cliCmd.AddCommand(udpCmd)
	cliCmd.AddCommand(connCmd)
	cliCmd.AddCommand(rootObjCmd)
	cliCmd.AddCommand(statCmd)
}

func getCLIRPCClient() (*node.RPCClient, error) {
	return node.NewRPCClient(rpcAddress)
}

// --- Feed commands ---

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Feed management commands",
}

func init() {
	feedCmd.AddCommand(
		&cobra.Command{
			Use:   "share <public-key>",
			Short: "Start sharing a feed",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				return r.Node().Share(pk)
			},
		},
		&cobra.Command{
			Use:   "unshare <public-key>",
			Short: "Stop sharing a feed",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				return r.Node().DontShare(pk)
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all shared feeds",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				list, err := r.Node().Feeds()
				if err != nil {
					return err
				}
				if len(list) == 0 {
					fmt.Fprintln(out, "  no feeds are shared") //nolint:errcheck
					return nil
				}
				for _, pk := range list {
					fmt.Fprintln(out, "  -", pk.Hex()) //nolint:errcheck
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "is-sharing <public-key>",
			Short: "Check if a feed is being shared",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				yep, err := r.Node().IsSharing(pk)
				if err != nil {
					return err
				}
				if yep {
					fmt.Fprintln(out, "  yes, it is") //nolint:errcheck
				} else {
					fmt.Fprintln(out, "  no, it is not") //nolint:errcheck
				}
				return nil
			},
		},
	)
}

// --- TCP commands ---

var tcpCmd = &cobra.Command{
	Use:   "tcp",
	Short: "TCP transport commands",
}

func init() {
	tcpCmd.AddCommand(
		&cobra.Command{
			Use:   "connect <address>",
			Short: "Connect to a TCP address",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				return r.TCP().Connect(args[0])
			},
		},
		&cobra.Command{
			Use:   "disconnect <address>",
			Short: "Disconnect from a TCP address",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				return r.TCP().Disconnect(args[0])
			},
		},
		&cobra.Command{
			Use:   "subscribe <address> <public-key>",
			Short: "Subscribe to a feed via TCP connection",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[1])
				if err != nil {
					return err
				}
				return r.TCP().Subscribe(args[0], pk)
			},
		},
		&cobra.Command{
			Use:   "unsubscribe <address> <public-key>",
			Short: "Unsubscribe from a feed via TCP connection",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[1])
				if err != nil {
					return err
				}
				return r.TCP().Unsubscribe(args[0], pk)
			},
		},
		&cobra.Command{
			Use:   "address",
			Short: "Show TCP listening address",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				addr, err := r.TCP().Address()
				if err != nil {
					return err
				}
				if addr == "" {
					fmt.Fprintln(out, "  doesn't listen") //nolint:errcheck
				} else {
					fmt.Fprintln(out, " "+addr) //nolint:errcheck
				}
				return nil
			},
		},
	)
}

// --- UDP commands ---

var udpCmd = &cobra.Command{
	Use:   "udp",
	Short: "UDP transport commands",
}

func init() {
	udpCmd.AddCommand(
		&cobra.Command{
			Use:   "connect <address>",
			Short: "Connect to a UDP address",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				return r.UDP().Connect(args[0])
			},
		},
		&cobra.Command{
			Use:   "disconnect <address>",
			Short: "Disconnect from a UDP address",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				return r.UDP().Disconnect(args[0])
			},
		},
		&cobra.Command{
			Use:   "subscribe <address> <public-key>",
			Short: "Subscribe to a feed via UDP connection",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[1])
				if err != nil {
					return err
				}
				return r.UDP().Subscribe(args[0], pk)
			},
		},
		&cobra.Command{
			Use:   "unsubscribe <address> <public-key>",
			Short: "Unsubscribe from a feed via UDP connection",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[1])
				if err != nil {
					return err
				}
				return r.UDP().Unsubscribe(args[0], pk)
			},
		},
		&cobra.Command{
			Use:   "address",
			Short: "Show UDP listening address",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				addr, err := r.UDP().Address()
				if err != nil {
					return err
				}
				if addr == "" {
					fmt.Fprintln(out, "  doesn't listen") //nolint:errcheck
				} else {
					fmt.Fprintln(out, " "+addr) //nolint:errcheck
				}
				return nil
			},
		},
	)
}

// --- Connection commands ---

var connCmd = &cobra.Command{
	Use:   "connection",
	Short: "Connection management commands",
}

func init() {
	connCmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List all connections",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				cs, err := r.Node().Connections()
				if err != nil {
					return err
				}
				printConnections(cs)
				return nil
			},
		},
		&cobra.Command{
			Use:   "list-by-feed <public-key>",
			Short: "List connections for a specific feed",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				cs, err := r.Node().ConnectionsOfFeed(pk)
				if err != nil {
					return err
				}
				printConnections(cs)
				return nil
			},
		},
	)
}

// --- Root object commands ---

var rootObjCmd = &cobra.Command{
	Use:   "root",
	Short: "Root object commands",
}

func init() {
	rootObjCmd.AddCommand(
		&cobra.Command{
			Use:   "info <public-key> <nonce> <seq>",
			Short: "Show info of a Root object",
			Args:  cobra.ExactArgs(3),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				nonce, err := strconv.ParseUint(args[1], 10, 64)
				if err != nil {
					return err
				}
				seq, err := strconv.ParseUint(args[2], 10, 64)
				if err != nil {
					return err
				}
				z, err := r.Root().Show(pk, nonce, seq)
				if err != nil {
					return err
				}
				printRoot(z)
				return nil
			},
		},
		&cobra.Command{
			Use:   "tree <public-key> <nonce> <seq>",
			Short: "Print tree of a Root object",
			Args:  cobra.ExactArgs(3),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				nonce, err := strconv.ParseUint(args[1], 10, 64)
				if err != nil {
					return err
				}
				seq, err := strconv.ParseUint(args[2], 10, 64)
				if err != nil {
					return err
				}
				tree, err := r.Root().Tree(pk, nonce, seq)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, " ", tree) //nolint:errcheck
				return nil
			},
		},
		&cobra.Command{
			Use:   "last <public-key>",
			Short: "Show info about last Root of a feed",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := getCLIRPCClient()
				if err != nil {
					return err
				}
				defer r.Close() //nolint:errcheck,gosec
				pk, err := pubKeyFromHex(args[0])
				if err != nil {
					return err
				}
				z, err := r.Root().Last(pk)
				if err != nil {
					return err
				}
				printRoot(z)
				return nil
			},
		},
	)
}

// --- Stat command ---

var statCmd = &cobra.Command{
	Use:   "stat",
	Short: "Show node statistics",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := getCLIRPCClient()
		if err != nil {
			return err
		}
		defer r.Close() //nolint:errcheck,gosec
		s, err := r.Node().Stat()
		if err != nil {
			return err
		}
		printStat(s)
		return nil
	},
}

// --- Interactive mode ---

func runInteractive(cmd *cobra.Command, args []string) {
	if rpcAddress == "" {
		fmt.Fprintln(os.Stderr, "empty address")
		os.Exit(1)
	}

	r, err := getCLIRPCClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer r.Close() //nolint:errcheck,gosec

	if execCmd != "" {
		if err := executeInteractiveCommand(r, execCmd); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	line := liner.NewLiner()
	defer line.Close() //nolint:errcheck,gosec

	line.SetCtrlCAborts(true)

	line.SetCompleter(func(line string) (c []string) {
		cmds := []string{
			"share feed ", "don't share feed ", "list feeds ", "is shareing ",
			"tcp connect ", "tcp disconnect ", "tcp subscribe ", "tcp unsubscribe ",
			"tcp address ",
			"udp connect ", "udp disconnect ", "udp subscribe ", "udp unsubscribe ",
			"udp address ",
			"connections ", "connections of feed ",
			"root info ", "root tree ", "last root ",
			"stat ", "help", "quit ", "exit ",
		}
		for _, n := range cmds {
			if strings.HasPrefix(n, strings.ToLower(line)) {
				c = append(c, n)
			}
		}
		return
	})

	if err := loadHistory(line); err != nil {
		fmt.Fprintln(os.Stderr, "error loading history:", err)
	}
	defer saveHistory(line)

	fmt.Fprintln(out, "enter 'help' to get help, use 'tab' to complete command")         //nolint:errcheck
	fmt.Fprintln(out, "tip: use subcommands directly (e.g., skywire cxo cli feed list)") //nolint:errcheck

	for {
		input, err := line.Prompt("> ")
		if err != nil {
			if err != liner.ErrPromptAborted {
				fmt.Fprintln(os.Stderr, "fatal error:", err)
				os.Exit(1)
			}
			return
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if input == "quit" || input == "exit" {
			fmt.Fprintln(out, "cya") //nolint:errcheck
			return
		}

		if input == "help" {
			printInteractiveHelp()
			line.AppendHistory(input)
			continue
		}

		if err := executeInteractiveCommand(r, input); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		line.AppendHistory(input)
	}
}

func executeInteractiveCommand(r *node.RPCClient, input string) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	switch {
	case matchPrefix(input, "share feed"):
		return execShareFeed(r, parts)
	case matchPrefix(input, "don't share feed"):
		return execDontShareFeed(r, parts)
	case matchPrefix(input, "list feeds"):
		return execListFeeds(r)
	case matchPrefix(input, "is shareing"):
		return execIsSharing(r, parts)
	case matchPrefix(input, "tcp connect"):
		return execTCPConnect(r, parts)
	case matchPrefix(input, "tcp disconnect"):
		return execTCPDisconnect(r, parts)
	case matchPrefix(input, "tcp subscribe"), matchPrefix(input, "tcp subsribe"):
		return execTCPSubscribe(r, parts)
	case matchPrefix(input, "tcp unsubscribe"):
		return execTCPUnsubscribe(r, parts)
	case matchPrefix(input, "tcp address"):
		return execTCPAddress(r)
	case matchPrefix(input, "udp connect"):
		return execUDPConnect(r, parts)
	case matchPrefix(input, "udp disconnect"):
		return execUDPDisconnect(r, parts)
	case matchPrefix(input, "udp subscribe"), matchPrefix(input, "udp subsribe"):
		return execUDPSubscribe(r, parts)
	case matchPrefix(input, "udp unsubscribe"):
		return execUDPUnsubscribe(r, parts)
	case matchPrefix(input, "udp address"):
		return execUDPAddress(r)
	case matchPrefix(input, "connections of feed"):
		return execConnectionsOfFeed(r, parts)
	case matchPrefix(input, "connections"):
		return execConnections(r)
	case matchPrefix(input, "root info"):
		return execRootInfo(r, parts)
	case matchPrefix(input, "root tree"):
		return execRootTree(r, parts)
	case matchPrefix(input, "last root"):
		return execLastRoot(r, parts)
	case matchPrefix(input, "stat"):
		return execStat(r)
	default:
		return fmt.Errorf("unknown command %q", input)
	}
}

func matchPrefix(input, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(input), prefix)
}

// --- Interactive command implementations ---

func execShareFeed(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: share feed <public-key>")
	}
	pk, err := pubKeyFromHex(parts[2])
	if err != nil {
		return err
	}
	return r.Node().Share(pk)
}

func execDontShareFeed(r *node.RPCClient, parts []string) error {
	if len(parts) < 4 {
		return errors.New("usage: don't share feed <public-key>")
	}
	pk, err := pubKeyFromHex(parts[3])
	if err != nil {
		return err
	}
	return r.Node().DontShare(pk)
}

func execListFeeds(r *node.RPCClient) error {
	list, err := r.Node().Feeds()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "  no feeds are shared") //nolint:errcheck
		return nil
	}
	for _, pk := range list {
		fmt.Fprintln(out, "  -", pk.Hex()) //nolint:errcheck
	}
	return nil
}

func execIsSharing(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: is shareing <public-key>")
	}
	pk, err := pubKeyFromHex(parts[2])
	if err != nil {
		return err
	}
	yep, err := r.Node().IsSharing(pk)
	if err != nil {
		return err
	}
	if yep {
		fmt.Fprintln(out, "  yes, it is") //nolint:errcheck
	} else {
		fmt.Fprintln(out, "  no, it is not") //nolint:errcheck
	}
	return nil
}

func execTCPConnect(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: tcp connect <address>")
	}
	return r.TCP().Connect(parts[2])
}

func execTCPDisconnect(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: tcp disconnect <address>")
	}
	return r.TCP().Disconnect(parts[2])
}

func execTCPSubscribe(r *node.RPCClient, parts []string) error {
	if len(parts) < 4 {
		return errors.New("usage: tcp subscribe <address> <public-key>")
	}
	pk, err := pubKeyFromHex(parts[3])
	if err != nil {
		return err
	}
	return r.TCP().Subscribe(parts[2], pk)
}

func execTCPUnsubscribe(r *node.RPCClient, parts []string) error {
	if len(parts) < 4 {
		return errors.New("usage: tcp unsubscribe <address> <public-key>")
	}
	pk, err := pubKeyFromHex(parts[3])
	if err != nil {
		return err
	}
	return r.TCP().Unsubscribe(parts[2], pk)
}

func execTCPAddress(r *node.RPCClient) error {
	addr, err := r.TCP().Address()
	if err != nil {
		return err
	}
	if addr == "" {
		fmt.Fprintln(out, "  doesn't listen") //nolint:errcheck
	} else {
		fmt.Fprintln(out, " "+addr) //nolint:errcheck
	}
	return nil
}

func execUDPConnect(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: udp connect <address>")
	}
	return r.UDP().Connect(parts[2])
}

func execUDPDisconnect(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: udp disconnect <address>")
	}
	return r.UDP().Disconnect(parts[2])
}

func execUDPSubscribe(r *node.RPCClient, parts []string) error {
	if len(parts) < 4 {
		return errors.New("usage: udp subscribe <address> <public-key>")
	}
	pk, err := pubKeyFromHex(parts[3])
	if err != nil {
		return err
	}
	return r.UDP().Subscribe(parts[2], pk)
}

func execUDPUnsubscribe(r *node.RPCClient, parts []string) error {
	if len(parts) < 4 {
		return errors.New("usage: udp unsubscribe <address> <public-key>")
	}
	pk, err := pubKeyFromHex(parts[3])
	if err != nil {
		return err
	}
	return r.UDP().Unsubscribe(parts[2], pk)
}

func execUDPAddress(r *node.RPCClient) error {
	addr, err := r.UDP().Address()
	if err != nil {
		return err
	}
	if addr == "" {
		fmt.Fprintln(out, "  doesn't listen") //nolint:errcheck
	} else {
		fmt.Fprintln(out, " "+addr) //nolint:errcheck
	}
	return nil
}

func execConnections(r *node.RPCClient) error {
	cs, err := r.Node().Connections()
	if err != nil {
		return err
	}
	printConnections(cs)
	return nil
}

func execConnectionsOfFeed(r *node.RPCClient, parts []string) error {
	if len(parts) < 4 {
		return errors.New("usage: connections of feed <public-key>")
	}
	pk, err := pubKeyFromHex(parts[3])
	if err != nil {
		return err
	}
	cs, err := r.Node().ConnectionsOfFeed(pk)
	if err != nil {
		return err
	}
	printConnections(cs)
	return nil
}

func execRootInfo(r *node.RPCClient, parts []string) error {
	if len(parts) < 5 {
		return errors.New("usage: root info <public-key> <nonce> <seq>")
	}
	pk, err := pubKeyFromHex(parts[2])
	if err != nil {
		return err
	}
	nonce, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return err
	}
	seq, err := strconv.ParseUint(parts[4], 10, 64)
	if err != nil {
		return err
	}
	z, err := r.Root().Show(pk, nonce, seq)
	if err != nil {
		return err
	}
	printRoot(z)
	return nil
}

func execRootTree(r *node.RPCClient, parts []string) error {
	if len(parts) < 5 {
		return errors.New("usage: root tree <public-key> <nonce> <seq>")
	}
	pk, err := pubKeyFromHex(parts[2])
	if err != nil {
		return err
	}
	nonce, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return err
	}
	seq, err := strconv.ParseUint(parts[4], 10, 64)
	if err != nil {
		return err
	}
	tree, err := r.Root().Tree(pk, nonce, seq)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, " ", tree) //nolint:errcheck
	return nil
}

func execLastRoot(r *node.RPCClient, parts []string) error {
	if len(parts) < 3 {
		return errors.New("usage: last root <public-key>")
	}
	pk, err := pubKeyFromHex(parts[2])
	if err != nil {
		return err
	}
	z, err := r.Root().Last(pk)
	if err != nil {
		return err
	}
	printRoot(z)
	return nil
}

func execStat(r *node.RPCClient) error {
	s, err := r.Node().Stat()
	if err != nil {
		return err
	}
	printStat(s)
	return nil
}

// --- Helpers ---

func pubKeyFromHex(pks string) (pk cipher.PubKey, err error) {
	var b []byte
	if b, err = hex.DecodeString(pks); err != nil {
		return
	}
	if len(b) != len(cipher.PubKey{}) {
		err = errors.New("invalid PubKey length")
		return
	}
	pk, err = cipher.NewPubKey(b)
	return
}

func printConnections(cs []string) {
	if len(cs) == 0 {
		fmt.Fprintln(out, "  no connections") //nolint:errcheck
		return
	}
	for _, c := range cs {
		fmt.Fprintln(out, " ", c) //nolint:errcheck
	}
}

func printRoot(z *registry.Root) {
	refs := make([]string, 0, len(z.Refs))
	for _, dr := range z.Refs {
		refs = append(refs, dr.Short())
	}
	//nolint:errcheck
	fmt.Fprintf(out, `  root  %s

    refs:       %#v

	descriptor: %q
	registry:   %s

	feed:       %s
	nonce:      %d
	seq:        %d

	time:       %v

	sig:        %s
	prev:       %s

`,
		z.Hash.Hex(),
		refs,
		string(z.Descriptor),
		z.Reg.String(),
		z.Pub.Hex(),
		z.Nonce,
		z.Seq,
		time.Unix(0, z.Time),
		z.Sig.Hex(),
		z.Prev.Hex(),
	)
}

func round(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

func printStat(s *node.Stat) {
	fmt.Fprintln(out, "  average filling duration:       ", s.Fillavg) //nolint:errcheck

	fmt.Fprintln(out, "  CXDS RPS:                       ", round(s.CXDS.RPS)) //nolint:errcheck
	fmt.Fprintln(out, "  CXDS WPS:                       ", round(s.CXDS.WPS)) //nolint:errcheck

	fmt.Fprintln(out, "  cache RPS:                      ", round(s.Cache.RPS)) //nolint:errcheck
	fmt.Fprintln(out, "  cache WPS:                      ", round(s.Cache.WPS)) //nolint:errcheck

	fmt.Fprintln(out, "  average cache cleaning duration:", s.CacheCleaning) //nolint:errcheck

	fmt.Fprintln(out, "  amount of cached objects:       ", //nolint:errcheck
		s.CacheObjects.Amount.String())
	fmt.Fprintln(out, "  volume of cached objects:       ", //nolint:errcheck
		s.CacheObjects.Volume.String())

	fmt.Fprintln(out, "  amount of all objects:          ", //nolint:errcheck
		s.AllObjects.Amount.String())
	fmt.Fprintln(out, "  volume of all objects:          ", //nolint:errcheck
		s.AllObjects.Volume.String())

	fmt.Fprintln(out, "  amount of used objects:         ", //nolint:errcheck
		s.UsedObjects.Amount.String())
	fmt.Fprintln(out, "  volume of used objects:         ", //nolint:errcheck
		s.UsedObjects.Volume.String())

	fmt.Fprintln(out, "  new Root objects per second:    ", s.RootsPerSecond) //nolint:errcheck

	if len(s.Feeds) == 0 {
		fmt.Fprintln(out, "  no feeds") //nolint:errcheck
		return
	}

	for pk, fs := range s.Feeds {
		fmt.Fprintln(out, " ", pk.Hex()) //nolint:errcheck

		if len(fs.Heads) == 0 {
			fmt.Fprintln(out, "    no heads") //nolint:errcheck
			continue
		}

		for nonce, hs := range fs.Heads {
			fmt.Fprintln(out, "    ", nonce) //nolint:errcheck

			switch hs.Len {
			case 0:
				fmt.Fprintln(out, "      no root objects") //nolint:errcheck
			case 1:
				fmt.Fprintln(out, "      last Root") //nolint:errcheck
				printRootStat(hs.Last)
			default:
				fmt.Fprintln(out, "      first Root") //nolint:errcheck
				printRootStat(hs.First)
				fmt.Fprintln(out, "      last Root") //nolint:errcheck
				printRootStat(hs.Last)
			}
		}
	}
}

func printRootStat(rs skyobject.RootStat) {
	fmt.Fprintln(out, "      hash:", rs.Hash.Hex()) //nolint:errcheck
	fmt.Fprintln(out, "      time:", rs.Time)       //nolint:errcheck
	fmt.Fprintln(out, "      seq: ", rs.Seq)        //nolint:errcheck
}

func printInteractiveHelp() {
	//nolint:errcheck
	fmt.Fprint(out, `

  share feed <public key>
    start sharing given feed
  don't share feed <public key>
    stop sharing given feed
  list feeds
    show all feeds the node share

  tcp connect <address>
    connect to tcp address
  tcp disconnect <connection address>
    close tcp connection
  tcp subscribe <connection address> <public key>
    subscribe to feed of peer
  tcp unsubscribe <connection address> <public key>
    unsubscribe from feed of peer
  tcp address
    tcp listening address

  udp connect <address>
    connect to udp address
  udp disconnect <connection address>
    close udp connection
  udp subscribe <connection address> <public key>
    subscribe to feed of peer
  udp unsubscribe <connection address> <public key>
    unsubscribe from feed of peer
  udp address
    udp listening address

  connections
    show all connections
  connections of feed <public key>
    show connections of given feed

  root info <public key> <nonce> <seq>
    show info of selected Root
  root tree <public key> <nonce> <seq>
    print tree of selected Root
  last root <public key>
    show info about last Root of given feed

  stat
    show statistic of node

  help
    show this help message

  quit or exit
    leave the cli

`)
}

func histroyFilePath() string {
	return filepath.Join(skyobject.DataDir(), historyFile)
}

func loadHistory(line *liner.State) error {
	hf := histroyFilePath()
	fl, err := os.Open(hf) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer fl.Close() //nolint:errcheck,gosec
	_, err = line.ReadHistory(fl)
	return err
}

func saveHistory(line *liner.State) {
	hf := histroyFilePath()
	fl, err := os.Create(hf) //nolint:gosec
	if err != nil {
		fmt.Fprintln(os.Stderr, "error saving history:", err)
		return
	}
	defer fl.Close() //nolint:errcheck,gosec
	if _, err = line.WriteHistory(fl); err != nil {
		fmt.Fprintln(os.Stderr, "error saving history:", err)
	}
}
