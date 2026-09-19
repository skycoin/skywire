//go:build !tinygo

// Package dmsgclient pkg/dmsg/dmsgclient/flags.go c1-net-dmsg
//
// CLI flag wiring (cobra) — excluded from TinyGo builds. The TinyGo wasm HV
// drives dmsg programmatically (StartDmsgSeeded), not via cobra flags.
package dmsgclient

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

var (
	// DmsgDiscURL is the dmsg discovery URL
	DmsgDiscURL = dmsg.DiscURL(false)

	// DmsgDiscAddr is the dmsg discovery dmsg address
	DmsgDiscAddr = dmsg.DiscAddr(false)

	// DmsgSessions is the default number of sessions i.e. servers to connect to
	DmsgSessions = 2

	// DmsgHTTPPath is the path to the dmsghttp-config.json which overrides embedded defaults
	DmsgHTTPPath string

	// UseDC use dmsg direct client with embedded dmsg server configuration and don't connect to discovery server
	UseDC = false

	// DmsgServerAddr specifies a specific dmsg server to connect through.
	// Format: pk@ip:port (e.g., 02a2d4c3...@45.79.124.73:30082)
	DmsgServerAddr string

	// NoRegister suppresses this tool's own dmsg discovery entry.
	//
	// A discovery entry is an INBOUND-reachability advertisement: "these are
	// the servers you can reach me through". A tool that only ever dials out —
	// dmsgcurl fetching a URL, dmsgip asking for an address, the dmsgweb
	// resolver, the dmsg-socks5 client half — opens no dmsg listener, so its
	// entry names servers for a destination that accepts nothing. It is a write
	// on the discovery, a row in its store and a TTL to expire, for an identity
	// that is usually ephemeral (these tools generate a keypair per run) and
	// gone in seconds.
	//
	// Not registering does NOT make a client undialable, and does not affect
	// its ability to dial or resolve anyone else — see dmsg.Config.NoRegister,
	// where registration and resolution are documented as independent axes.
	//
	// Binaries that DO accept inbound dmsg dials (dmsghttp, dmsgwebsrv,
	// dmsg-socks5 serve) leave this false: their entry is the whole point.
	NoRegister = false

	// DmsgAttach is the local visor dmsg-relay acceptor to attach to instead
	// of dialing dmsg servers: a unix socket path, or "tcp://host:port" for a
	// loopback listener. The tool keeps its own key — that is the whole point
	// — and holds no server sessions and no discovery entry while attached.
	// See StartDmsgLocalRelay.
	DmsgAttach string
)

// AttachTarget splits DmsgAttach into (network, address). A bare path is a
// unix socket — the default and the one with a filesystem gate; "tcp://" opts
// into the loopback listener, which the visor only serves when it has an
// explicit key allowlist.
func AttachTarget(s string) (network, addr string) {
	if rest, ok := strings.CutPrefix(s, "tcp://"); ok {
		return "tcp", rest
	}
	return "unix", strings.TrimPrefix(s, "unix://")
}

// InitFlags is used to set command flags for the above variables.
//
// The plain-HTTP discovery flags (-Z/--http, -U/--disc-url) were removed: the
// deployment is dmsg-only and the clearnet dmsg-discovery HTTP frontend is gone
// (404), so connecting to discovery over http no longer works. Discovery is
// reached over dmsg via -A/--disc-addr (dmsg://<pk>:<port>), which is the
// default; -B/--direct uses the embedded server set with no discovery at all.
func InitFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&UseDC, "direct", "B", UseDC, "use dmsg-direct client & don't connect to DMSG Discovery")
	cmd.Flags().StringVarP(&DmsgDiscAddr, "disc-addr", "A", DmsgDiscAddr, "DMSG Discovery dmsg address")
	cmd.Flags().StringVarP(&DmsgHTTPPath, "dmsgconf", "D", "", "dmsghttp-config path")
	cmd.Flags().IntVarP(&DmsgSessions, "sess", "e", DmsgSessions, "number of DMSG Servers to connect to")
	cmd.Flags().StringVarP(&DmsgServerAddr, "srv", "S", "", "connect via specific dmsg server `pk@ip:port`")
	cmd.Flags().StringVar(&DmsgAttach, "attach", "", "attach to a local visor's dmsg relay `socket` (holds no server sessions, publishes no entry, keeps this key)")
}

// ParseServerAddr parses the --srv flag value into a disc.Entry.
// Format: pk@ip:port
func ParseServerAddr(s string) (*disc.Entry, error) {
	parts := strings.SplitN(s, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid server address %q, expected pk@ip:port", s)
	}
	var pk cipher.PubKey
	if err := pk.Set(parts[0]); err != nil {
		return nil, fmt.Errorf("invalid server public key: %w", err)
	}
	return &disc.Entry{
		Version: "0.0.1",
		Static:  pk,
		Server:  &disc.Server{Address: parts[1], AvailableSessions: 2048},
	}, nil
}

// InitConfig is used to set command flags for the above variables
func InitConfig() error {
	var err error
	if DmsgHTTPPath != "" {
		dmsg.DmsghttpJSON, err = os.ReadFile(DmsgHTTPPath) //nolint
		if err != nil {
			return err
		}
		err = dmsg.InitConfig()
		if err != nil {
			return err
		}
	}
	return err

}
