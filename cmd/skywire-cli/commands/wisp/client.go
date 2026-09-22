// Package cliwisp cmd/skywire-cli/commands/wisp/client.go c4-vis-cli
//
// `skywire cli wisp client` — the consuming end: dial someone else's Wisp
// backend and present it locally as a SOCKS5 proxy, so anything that speaks
// SOCKS5 (a browser, curl, a whole network namespace) egresses through it.
//
// --proxy is what makes this interesting on a mesh. The WebSocket handshake
// itself is dialed through a SOCKS5 proxy, so pointing --proxy at the local
// skysocks-client puts the Wisp session on a route to an exit: the backend is
// reached over skywire, and the backend's own egress is wherever it chose.
// Neither end has to be on the clearnet for the two to meet.
package cliwisp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/wisp"
)

var (
	wispClientURL    string
	wispClientListen string
	wispClientProxy  string
)

func init() {
	clientCmd.Flags().StringVarP(&wispClientURL, "url", "u", "", "wisp endpoint to dial, ws:// or wss:// (required)")
	clientCmd.Flags().StringVarP(&wispClientListen, "listen", "l", "127.0.0.1:1081", "address to serve the local SOCKS5 proxy on")
	clientCmd.Flags().StringVar(&wispClientProxy, "proxy", "", "SOCKS5 proxy to dial the websocket itself through — e.g. the local skysocks-client, to reach the backend over skywire")
}

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Use a remote Wisp backend as a local SOCKS5 proxy",
	Long: `Dial a Wisp backend and present it locally as a SOCKS5 proxy.

Every SOCKS5 CONNECT becomes a Wisp stream on one shared WebSocket, so a
browser's several dozen connections cost one socket rather than several dozen.
Names are never resolved here: the hostname is passed through to the backend,
which resolves it at the far end.

UDP ASSOCIATE is served too, since a Wisp session carries datagrams as well as
streams. The proxy binds the relay socket the application is promised and
opens one Wisp UDP stream per destination behind it, reaping the ones that
fall idle; the association ends with its control connection, as RFC 1928 says.
A backend that does not advertise the UDP extension gets a refusal rather than
an association that swallows everything sent to it.

--proxy dials the websocket through a SOCKS5 proxy of its own. Pointed at the
local skysocks-client it puts the Wisp session on a route to an exit, so the
backend is reached over skywire rather than the clearnet.

The session is redialed on demand: if the backend restarts, the next
connection through the proxy brings it back rather than needing this command
restarted.

Examples:
  skywire cli wisp client -u ws://127.0.0.1:6001/wisp
  skywire cli wisp client -u wss://example.org/wisp -l 127.0.0.1:1081
  skywire cli wisp client -u ws://10.0.0.5:6001/wisp --proxy 127.0.0.1:1080

  # then point anything that speaks SOCKS5 at it:
  curl -x socks5h://127.0.0.1:1081 https://api.ipify.org`,
	Run: func(cmd *cobra.Command, _ []string) {
		if wispClientURL == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--url is required"))
		}
		if !strings.HasPrefix(wispClientURL, "ws://") && !strings.HasPrefix(wispClientURL, "wss://") {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--url must be ws:// or wss://, got %q", wispClientURL))
		}

		log := logging.MustGetLogger("wisp-client")

		httpClient, err := viaClient(wispClientProxy)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		sess := &session{
			cfg: wisp.ClientConfig{
				URL:        wispClientURL,
				HTTPClient: httpClient,
				Log:        log,
			},
			log: log,
		}

		// Dial once up front so a typo or a dead backend shows now
		// rather than on the first connection through the proxy. A
		// failure here is not fatal: a route to the backend may still
		// be coming up, and the lazy redial will find it.
		var status string
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		c, err := sess.get(ctx)
		cancel()
		if err != nil {
			status = fmt.Sprintf("not yet connected (%v) — will retry on demand", err)
		} else {
			status = fmt.Sprintf("connected, wisp v%d, buffer %d packets, udp %s",
				c.Version(), c.Buffer(), yesNo(c.UDPSupported()))
		}

		srv := &wisp.SocksServer{
			Session: sess.get,
			Log:     log,
		}

		var b strings.Builder
		fmt.Fprintf(&b, "socks5 proxy on %s\n", wispClientListen)
		fmt.Fprintf(&b, "  backend: %s\n", wispClientURL)
		if wispClientProxy != "" {
			fmt.Fprintf(&b, "  via:     %s\n", wispClientProxy)
		}
		fmt.Fprintf(&b, "  status:  %s\n", status)
		internal.PrintOutput(cmd.Flags(), b.String(), b.String())

		l, err := net.Listen("tcp", wispClientListen)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		// Serve only ever returns on failure.
		internal.PrintFatalError(cmd.Flags(), srv.Serve(l))
	},
}

// session holds the current Wisp session and redials it on demand.
//
// Redialing lazily rather than from a supervisor goroutine keeps a dead
// backend from being hammered while nothing is asking for it, and means the
// first connection after the backend returns is the one that revives it.
type session struct {
	cfg wisp.ClientConfig
	log *logging.Logger

	mu      sync.Mutex
	cur     *wisp.Client
	nextTry time.Time
	backoff time.Duration
}

const (
	sessionBackoffMin = time.Second
	sessionBackoffMax = 30 * time.Second
)

// get returns a live session, dialing one if the current session has ended.
func (s *session) get(ctx context.Context) (*wisp.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cur != nil && s.cur.Err() == nil {
		return s.cur, nil
	}
	if s.cur != nil {
		s.log.WithError(s.cur.Err()).Debug("session ended, redialing")
		s.cur = nil
	}
	// A backend that is down would otherwise be dialed once per
	// connection, which for a browser is dozens of handshakes a second.
	if now := time.Now(); now.Before(s.nextTry) {
		return nil, fmt.Errorf("wisp backend unreachable, next attempt in %s", time.Until(s.nextTry).Round(time.Millisecond))
	}

	c, err := wisp.Dial(ctx, s.cfg)
	if err != nil {
		if s.backoff == 0 {
			s.backoff = sessionBackoffMin
		} else if s.backoff < sessionBackoffMax {
			s.backoff *= 2
		}
		if s.backoff > sessionBackoffMax {
			s.backoff = sessionBackoffMax
		}
		s.nextTry = time.Now().Add(s.backoff)
		return nil, err
	}

	s.backoff = 0
	s.nextTry = time.Time{}
	s.cur = c
	return c, nil
}

// viaClient builds the HTTP client that performs the websocket handshake,
// optionally through a SOCKS5 proxy.
func viaClient(via string) (*http.Client, error) {
	if via == "" {
		return nil, nil //nolint:nilnil // no client means "use the default", which is what websocket.Dial wants
	}
	d, err := proxy.SOCKS5("tcp", via, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("--proxy %s: %w", via, err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("--proxy %s: dialer does not support contexts", via)
	}
	return &http.Client{
		Transport: &http.Transport{DialContext: cd.DialContext},
	}, nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
