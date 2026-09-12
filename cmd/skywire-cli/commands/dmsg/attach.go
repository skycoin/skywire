// Package clidmsg cmd/skywire-cli/commands/dmsg/attach.go c4-vis-cli
// `skywire cli dmsg attach` — inspect and exercise a local visor's dmsg relay
// acceptor (pkg/visor/init_dmsg_relay_local.go).
//
// Attaching is how a standalone dmsg process stops holding dmsg server sessions
// WITHOUT changing its key: it gets one session, to the visor beside it, over a
// unix socket, and its streams are forwarded over that visor's sessions and
// transports. This command is the two checks an operator needs before trusting
// that path:
//
//	skywire cli dmsg attach                 # what is on the other end of the socket?
//	skywire cli dmsg attach --sk <key>      # would MY key be admitted?
//
// The first needs no identity at all — it reads the acceptor's hello and hangs
// up — which is deliberate: verifying a socket must not mint a key, and a
// deployment that has paid for hundreds of throwaway identities does not need
// another source of them.
//
// The end-to-end path with real traffic is the --attach flag on the standalone
// tools: `skywire cli dmsg curl --attach <socket> dmsg://<pk>:80/...` fetches
// over the visor's sessions under the caller's own key.
package clidmsg

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// attachSocket is the shared --attach value: a unix socket path, or
	// "tcp://host:port" for a visor serving the loopback listener. Read by
	// startDmsgClient (curl.go) so every standalone subcommand that bootstraps
	// its own client can run attached instead.
	attachSocket string
	attachDial   string
)

func init() {
	attachCmd.Flags().SortFlags = false
	attachCmd.Flags().VarP(&sk, "sk", "s",
		"secret key to attach with — omit to only probe the acceptor's identity")
	attachCmd.Flags().StringVar(&attachDial, "dial", "",
		"after attaching, dial `<pk>:<port>` over the relay to prove the path end to end")
	attachCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")
	RootCmd.AddCommand(attachCmd)
}

// defaultAttachSocket is where a visor with `dmsg.local_relay.socket` unset
// binds its acceptor. Kept in step with pkg/visor's
// defaultLocalRelaySocketName; a wrong guess here costs one clear "no such
// file" and not a misdial, so it is not worth an RPC round-trip to the visor.
func defaultAttachSocket() string {
	return filepath.Join(skyenv.LocalPath, "dmsg_relay.sock")
}

var attachCmd = &cobra.Command{
	Use:   "attach [socket]",
	Short: "Inspect a local visor's dmsg relay acceptor",
	Long: `Inspect a local visor's dmsg relay acceptor.

A process attached to the acceptor holds ONE dmsg session — to the visor, over
this socket — instead of sessions to dmsg servers, publishes no discovery
entry, and keeps its own key. Its streams ride the visor's sessions and
transports.

With no --sk this only reads the acceptor's identity and hangs up. With --sk it
completes the attach, which is what proves the key is admitted: the visor's
allowed_keys check runs after the session handshake has proven the key.

The socket defaults to <local_path>/dmsg_relay.sock. Prefix a "tcp://" address
instead to reach a visor serving the loopback listener.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := defaultAttachSocket()
		if len(args) == 1 {
			target = args[0]
		}
		network, addr := dmsgclient.AttachTarget(target)
		log := logging.MustGetLogger("dmsg:attach")
		if lvl, err := logging.LevelFromString(logLvl); err == nil {
			logging.SetLevel(lvl)
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		if sk.Null() {
			relayPK, port, err := dmsgclient.LocalRelayIdentity(ctx, network, addr)
			if err != nil {
				return err
			}
			cmd.Printf("acceptor: %s://%s\nvisor:    %s\nport:     %d\n", network, addr, relayPK, port)
			cmd.Println("pass --sk to complete an attach and confirm the key is admitted")
			return nil
		}

		pk, err := sk.PubKey()
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}
		dmsgC, stop, relayPK, err := dmsgclient.StartDmsgLocalRelay(ctx, log, pk, sk, network, addr)
		if err != nil {
			return err
		}
		defer stop()

		cmd.Printf("attached: %s\nvisor:    %s\n", pk, relayPK)
		for _, ses := range dmsgC.AllSessions() {
			cmd.Printf("session:  %s (%s)\n", ses.RemotePK(), ses.Protocol())
		}
		if attachDial == "" {
			return nil
		}
		dst, err := parseAttachDial(attachDial)
		if err != nil {
			return err
		}
		stream, err := dmsgC.DialStream(ctx, dst)
		if err != nil {
			return fmt.Errorf("dial %s over the relay: %w", dst, err)
		}
		defer stream.Close() //nolint:errcheck
		cmd.Printf("dialed:   %s over the relay\n", dst)
		return nil
	},
}

// parseAttachDial parses the --dial "<pk>:<port>" destination.
func parseAttachDial(s string) (dmsg.Addr, error) {
	var addr dmsg.Addr
	host, portStr, err := splitLastColon(s)
	if err != nil {
		return addr, err
	}
	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return addr, fmt.Errorf("--dial %q: %w", s, err)
	}
	var port uint16
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port == 0 {
		return addr, fmt.Errorf("--dial %q: bad port", s)
	}
	return dmsg.Addr{PK: pk, Port: port}, nil
}

// splitLastColon splits "<pk>:<port>" on its final colon.
func splitLastColon(s string) (string, string, error) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("%q is not <pk>:<port>", s)
}
