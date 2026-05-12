// Package commands cmd/apps/skychat/skychat.go
package commands

import (
	"context"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var r = netutil.NewRetrier(nil, 50*time.Millisecond, netutil.DefaultMaxBackoff, 5, 2)

// Wire-protocol constants for skychat's peer-to-peer messages.
//
// Pre-2026-05-12 the protocol was "one conn.Write = one conn.Read":
// every message was sent as a raw byte slice and the receiver
// assumed each Read returned exactly one message. That held for
// dmsg (noise-framed up to 4 KB) but broke on the skynet route
// path — appnet.directConn wraps transport.VStream, which is a
// TCP-style stream that can split a single Write across multiple
// Reads at arbitrary boundaries depending on route MTU. A
// 600-byte chat message would arrive as two "messages" on the
// receiver and the second half would surface as a separate chat
// entry, looking to operators like the message was truncated.
//
// New protocol: length-prefixed frames. Each message is a 4-byte
// big-endian length followed by exactly that many bytes of
// payload. Old binaries can no longer talk to new ones — peers
// must update together. The pair-control envelope (JSON `{type:
// "pair-invite" | ...}`) keeps the same on-the-wire bytes; only
// the framing around it changed.
const skychatMaxFrameSize = 64 * 1024

// framedConn wraps an appnet conn with length-prefixed framing
// and a write mutex. The write mutex matters because two
// callers (the HTTP /message handler and the pair-control
// sender) can race to write to the same underlying conn — and
// with framing, interleaving the length prefix of one message
// with the payload of another would desync the receiver
// permanently. The read path has a single owner (handleConn)
// so no read mutex is needed.
type framedConn struct {
	net.Conn
	writeMu sync.Mutex
}

func newFramedConn(c net.Conn) *framedConn { return &framedConn{Conn: c} }

// WriteFrame writes a length-prefixed message. Returns an error
// if the payload is empty or exceeds skychatMaxFrameSize.
func (c *framedConn) WriteFrame(payload []byte) error {
	if len(payload) == 0 {
		return errors.New("skychat: empty payload")
	}
	if len(payload) > skychatMaxFrameSize {
		return fmt.Errorf("skychat: payload %d > max %d", len(payload), skychatMaxFrameSize)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := c.Conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Conn.Write(payload)
	return err
}

// ReadFrame reads exactly one length-prefixed message. Rejects
// frames over skychatMaxFrameSize so a malicious or out-of-sync
// peer can't allocate gigabytes of memory by claiming a giant
// length.
func (c *framedConn) ReadFrame() ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.Conn, hdr[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length == 0 {
		return nil, errors.New("skychat: zero-length frame")
	}
	if length > skychatMaxFrameSize {
		return nil, fmt.Errorf("skychat: frame %d > max %d (peer running old unframed protocol?)", length, skychatMaxFrameSize)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.Conn, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

var (
	addr      string
	appCl     *app.Client
	appLog    func(format string, args ...interface{}) // App logger function
	hub       *sseHub                                  // SSE broadcast registry; see sse.go-like helpers below
	conns     map[cipher.PubKey]*framedConn            // Chat connections
	connsMu   sync.Mutex
	appPort   uint16
	useSkynet bool
	useDmsg   bool

	// Optional HTTP password gate. When --password-file points at a
	// file containing a bcrypt hash, every HTTP endpoint requires
	// matching basic auth (or the hypervisor's internal-proxy
	// bypass token, see auth.go). Empty file or missing flag →
	// no auth, current behavior.
	passwordFile  string
	internalToken string

	// Persistence (Phase 1) — all off by default.
	persistEnabled       bool
	persistDBPath        string
	persistMaxMsgSize    int
	persistPerPeerRate   int
	persistPerPeerCap    int
	persistTotalCapMB    int
	persistTTLDays       int
	persistWhitelistFile string
	persistSeedCount     int

	// historyStore is nil when persistence is disabled.
	historyStore history.Store
)

// the go embed static points to skywire/cmd/apps/skychat/static

//go:embed static
var embededFiles embed.FS

// sseSubscriberBufSize is the per-client outbound message buffer
// depth. A slow SSE client (or a stalled browser tab) drops messages
// once its buffer is full rather than blocking the producer; missed
// messages are recoverable from history (when persistence is on) or
// just dropped (when off).
const sseSubscriberBufSize = 64

// sseHub fans messages out to every connected SSE client. The
// previous implementation used a single unbuffered channel, which
// meant exactly ONE consumer received each message — when more than
// one tab was open, or a stale handler was leaked, every other tab
// silently lost messages. The hub registers a per-client channel on
// connect and broadcasts to all of them on each message.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan string]struct{})}
}

// subscribe registers a fresh client channel and returns it plus an
// unsubscribe func the caller MUST invoke on shutdown. The channel
// is buffered so a producer never blocks on a slow consumer.
func (h *sseHub) subscribe() (<-chan string, func()) {
	ch := make(chan string, sseSubscriberBufSize)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// clientCount returns how many SSE subscribers are currently
// connected. Used by /status for operator health probes.
func (h *sseHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// broadcast sends msg to every connected client. Drops to clients
// whose buffer is full — bounded fan-out keeps a single stalled
// client from holding back the whole stream.
func (h *sseHub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func init() {
	launcher.RegisterApp("skychat", RunSkychat)
	RootCmd.Flags().StringVar(&addr, "addr", ":8001", "address to bind (default: localhost-only); use \"*:PORT\" to bind on all interfaces")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().BoolVar(&useSkynet, "skynet", true, "listen on skynet network")
	RootCmd.Flags().BoolVar(&useDmsg, "dmsg", true, "listen on dmsg network")
	RootCmd.Flags().StringVar(&passwordFile, "password-file", "", "path to a file containing a bcrypt hash; when set, gates HTTP endpoints with basic auth")
	RootCmd.Flags().StringVar(&internalToken, "internal-token", "", "shared secret used by the hypervisor's reverse proxy to bypass the password gate; managed automatically by the visor")

	// Persistence flags (Phase 1). All default off; when --persist is set,
	// the others fall back to conservative defaults.
	RootCmd.Flags().BoolVar(&persistEnabled, "persist", false, "persist chat history to a local BoltDB (off by default)")
	RootCmd.Flags().StringVar(&persistDBPath, "persist-db", "", "path to the BoltDB file (default: <work-dir>/skychat-history.db)")
	RootCmd.Flags().IntVar(&persistMaxMsgSize, "persist-max-size", 4096, "maximum persisted message size in bytes")
	RootCmd.Flags().IntVar(&persistPerPeerRate, "persist-per-peer-rate", 20, "persisted messages per minute per peer (rate limit)")
	RootCmd.Flags().IntVar(&persistPerPeerCap, "persist-per-peer-cap", 500, "maximum persisted messages per peer (FIFO eviction)")
	RootCmd.Flags().IntVar(&persistTotalCapMB, "persist-total-cap", 10, "total persisted storage cap in MB")
	RootCmd.Flags().IntVar(&persistTTLDays, "persist-ttl", 30, "days to keep persisted messages before sweep (0 disables)")
	RootCmd.Flags().StringVar(&persistWhitelistFile, "persist-whitelist", "", "path to file with one peer PK per line; if set, only these peers are persisted")
	RootCmd.Flags().IntVar(&persistSeedCount, "persist-seed", 50, "number of recent messages to seed new SSE clients with (0 disables)")

	// Pairing flags. Off by default so the legacy plain-text DM path
	// (used by the e2e CI tests) is unaffected. When --pair-enable is
	// on, skychat dials the local visor's RPC and exposes the
	// chat-pair feed manager over HTTP + the structured pair-invite
	// / pair-ack protocol over the legacy direct path.
	RootCmd.Flags().BoolVar(&pairEnable, "pair-enable", false, "enable per-partner CXO pair feeds (HTTP /pair endpoints + handshake)")
	RootCmd.Flags().StringVar(&pairRPCAddr, "pair-rpc", "localhost:3435", "visor RPC address used by the pair manager")
	RootCmd.Flags().DurationVar(&pairPollInterval, "pair-poll-interval", time.Second, "how often skychat drains the visor's pair-message inbox onto the SSE stream")
}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use:                   "skychat",
	Short:                 "skywire chat application",
	Long:                  calvin.AsciiFont("skychat"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()

		if err := RunSkychat(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// RunSkychat runs the skychat app logic. This can be called from the visor or from the CLI.
func RunSkychat(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		// Create independent FlagSet for parsing without initialization cycle
		fs := pflag.NewFlagSet("skychat", pflag.ContinueOnError)
		fs.StringVar(&addr, "addr", ":8001", "address to bind")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		fs.BoolVar(&useSkynet, "skynet", true, "listen on skynet")
		fs.BoolVar(&useDmsg, "dmsg", true, "listen on dmsg")
		fs.StringVar(&passwordFile, "password-file", "", "path to bcrypt hash for HTTP basic auth")
		fs.StringVar(&internalToken, "internal-token", "", "hypervisor proxy bypass token")
		fs.BoolVar(&persistEnabled, "persist", false, "persist chat history to BoltDB")
		fs.StringVar(&persistDBPath, "persist-db", "", "path to BoltDB file")
		fs.IntVar(&persistMaxMsgSize, "persist-max-size", 4096, "max message size bytes")
		fs.IntVar(&persistPerPeerRate, "persist-per-peer-rate", 20, "per-peer rate limit / min")
		fs.IntVar(&persistPerPeerCap, "persist-per-peer-cap", 500, "per-peer message cap")
		fs.IntVar(&persistTotalCapMB, "persist-total-cap", 10, "total storage cap in MB")
		fs.IntVar(&persistTTLDays, "persist-ttl", 30, "days to keep persisted messages")
		fs.StringVar(&persistWhitelistFile, "persist-whitelist", "", "whitelist file path")
		fs.IntVar(&persistSeedCount, "persist-seed", 50, "messages to seed SSE clients with")
		fs.BoolVar(&pairEnable, "pair-enable", false, "enable per-partner CXO pair feeds")
		fs.StringVar(&pairRPCAddr, "pair-rpc", "localhost:3435", "visor RPC address for pair manager")
		fs.DurationVar(&pairPollInterval, "pair-poll-interval", time.Second, "pair inbox poll interval")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	// Wrap context with cancel to allow graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	appCl = app.NewClient(nil)
	defer appCl.Close()

	// Set up app logger
	appLog = func(format string, args ...interface{}) {
		appCl.Log().Infof(format, args...)
	}

	appLog("Build info: %s", buildinfo.Version())
	appLog("Successfully started skychat.")

	if persistEnabled {
		if err := openHistoryStore(); err != nil {
			appLog("Failed to open history store: %v — continuing in ephemeral mode", err)
		} else {
			defer func() {
				if historyStore != nil {
					if err := historyStore.Close(); err != nil {
						appLog("history store close: %v", err)
					}
				}
			}()
		}
	}

	hub = newSSEHub()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
		setAppPort(appCl, port)
	}

	conns = make(map[cipher.PubKey]*framedConn)

	if useSkynet {
		go listenLoop(appnet.TypeSkynet, port)
	}
	if useDmsg {
		go listenLoop(appnet.TypeDmsg, port)
	}
	if !useSkynet && !useDmsg {
		appLog("Warning: no network types enabled, skychat will not accept connections")
	}

	if runtime.GOOS == "windows" {
		ipcClient, err := ipc.StartClient(skyenv.SkychatName, nil)
		if err != nil {
			appLog("Error creating ipc server for skychat client: %v", err)
			setAppError(appCl, err)
			return err
		}
		go handleIPCSignal(ipcClient)
	}

	connectPairRPC()
	startPairPoller(ctx)
	defer stopPairPoller()

	// Wire optional password protection. If passwordFile is empty or
	// the file is missing, requireAuth* are no-ops.
	if err := loadSkychatPassword(passwordFile); err != nil {
		appLog("password file load: %v — continuing without auth", err)
	}
	setSkychatInternalToken(internalToken)

	http.Handle("/", requireAuth(http.FileServer(getFileSystem())))
	http.HandleFunc("/message", requireAuthFunc(messageHandler(ctx)))
	http.HandleFunc("/sse", requireAuthFunc(sseHandler))
	http.HandleFunc("/history", requireAuthFunc(historyHandler))
	http.HandleFunc("/history/peers", requireAuthFunc(historyPeersHandler))
	http.HandleFunc("/status", requireAuthFunc(statusHandler))
	registerPairHTTPHandlers(ctx)

	url := ""
	address := addr
	if len(address) >= 2 && address[:2] == "*:" {
		url = "0.0.0.0" + address[1:]
	} else if len(address) >= 1 && address[:1] == ":" {
		url = "127.0.0.1" + address
	} else if host, port, err := net.SplitHostPort(address); err == nil && host != "" && port != "" {
		url = address
	} else {
		url = "127.0.0.1:8001"
	}

	appLog("Serving HTTP on %s", url)

	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)

		go func() {
			select {
			case <-termCh:
				setAppStatus(appCl, appserver.AppDetailedStatusStopped)
				cancel()
			case <-ctx.Done():
				setAppStatus(appCl, appserver.AppDetailedStatusStopped)
				return
			}
		}()
	}

	setAppStatus(appCl, appserver.AppDetailedStatusRunning)
	srv := &http.Server{
		Addr:         url,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			appLog("HTTP server error: %v", err)
			setAppError(appCl, err)
			return err
		}
	case <-ctx.Done():
		setAppStatus(appCl, appserver.AppDetailedStatusStopped)
		if err := srv.Shutdown(context.Background()); err != nil {
			return err
		}
	}

	return nil
}

func listenLoop(netType appnet.Type, appPort routing.Port) {
	l, err := appCl.Listen(netType, appPort)
	if err != nil {
		appLog("Error listening network %v on port %d: %v", netType, appPort, err)
		setAppError(appCl, err)
		return
	}

	appLog("Listening on %s network, port %d", netType, appPort)

	for {
		appCl.Log().Debugf("Accepting skychat conn on %s...", netType)
		conn, err := l.Accept()
		if err != nil {
			appLog("Failed to accept conn on %s: %v", netType, err)
			return
		}
		appCl.Log().Debugf("Accepted skychat conn on %s", netType)

		raddr := conn.RemoteAddr().(appnet.Addr)
		fc := newFramedConn(conn)
		connsMu.Lock()
		conns[raddr.PubKey] = fc
		connsMu.Unlock()
		appLog("Accepted skychat conn on %s from %s via %s", conn.LocalAddr(), raddr.PubKey, netType)

		go handleConn(fc)
	}
}

func handleConn(conn *framedConn) {
	raddr := conn.RemoteAddr().(appnet.Addr)
	for {
		payload, err := conn.ReadFrame()
		if err != nil {
			appLog("Failed to read packet: %v", err)
			connsMu.Lock()
			delete(conns, raddr.PubKey)
			connsMu.Unlock()
			return
		}

		// First, try the pair-control envelope. Recognized types
		// (pair-invite / pair-ack) are consumed here and not surfaced
		// as chat messages; everything else falls through to the
		// legacy plain-text path so the existing CI tests are
		// unaffected.
		if handlePairControlFrame(context.Background(), raddr.PubKey, payload) {
			continue
		}

		text := string(payload)
		peerPK := raddr.PubKey.Hex()

		// Persist (best-effort, never blocks ephemeral path).
		persistMessage(history.Message{
			Peer:      peerPK,
			From:      peerPK,
			Outgoing:  false,
			Text:      text,
			Timestamp: time.Now().UTC(),
		})

		// Include the network field so listen --net can filter
		// inbound just as it can filter outbound. Without this, SSE
		// events for incoming messages carry no transport tag and any
		// consumer-side --net filter drops them all.
		//
		// "dir" is a string ("in"|"out"|... future "relay"|"group-in"|
		// "group-out") rather than an "outgoing" bool so the schema
		// stays extensible as group / relay flows land without a
		// breaking wire-version bump.
		clientMsg, err := json.Marshal(map[string]interface{}{
			"sender":  peerPK,
			"message": text,
			"network": string(raddr.Net),
			"dir":     "in",
		})
		if err != nil {
			appLog("Failed to marshal json: %v", err)
		}
		hub.broadcast(string(clientMsg))
	}
}

func messageHandler(ctx context.Context) func(w http.ResponseWriter, rreq *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {

		data := map[string]string{}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(data["recipient"])); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Determine network type - default to skynet, allow dmsg
		netType := appnet.TypeSkynet
		if network, ok := data["network"]; ok {
			switch network {
			case "dmsg":
				netType = appnet.TypeDmsg
			case "skynet":
				netType = appnet.TypeSkynet
			default:
				http.Error(w, "invalid network type: use 'skynet' or 'dmsg'", http.StatusBadRequest)
				return
			}
		}

		addr := appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   1,
		}

		// Self-send detection — log it clearly so the user can tell self
		// traffic apart from peer traffic in the visor log. A dmsg self-
		// dial loops back through the visor's own delegated dmsg server:
		// the server bridges a new outbound yamux stream back to the same
		// client, and the local listener accepts it. The dial here is
		// the real network path — no local short-circuit. Same for
		// skynet (router builds a 2-hop self-ping loopback).
		isSelf := pk == appCl.Config().VisorPK
		if isSelf {
			appCl.Log().Infof("Self-send via %s on port %d", netType, addr.Port)
		}

		connsMu.Lock()
		conn, ok := conns[pk]
		connsMu.Unlock()

		if !ok {
			var raw net.Conn
			err := r.Do(ctx, func() error {
				c, dialErr := appCl.Dial(addr)
				if dialErr != nil {
					return dialErr
				}
				raw = c
				return nil
			})
			if err != nil {
				if isSelf {
					appCl.Log().WithError(err).Errorf("Self-dial via %s failed", netType)
				} else {
					appCl.Log().WithError(err).Warnf("Dial to %s via %s failed", pk, netType)
				}
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			conn = newFramedConn(raw)
			connsMu.Lock()
			conns[pk] = conn
			connsMu.Unlock()

			go handleConn(conn)
		}

		err := conn.WriteFrame([]byte(data["message"]))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			connsMu.Lock()
			delete(conns, pk)
			connsMu.Unlock()

			return
		}

		// Persist outgoing message (best-effort).
		persistMessage(history.Message{
			Peer:      pk.Hex(),
			Outgoing:  true,
			Text:      data["message"],
			Timestamp: time.Now().UTC(),
		})

		// Mirror the outgoing message into the SSE stream so headless
		// listeners (skywire-cli skychat listen) see a complete
		// transcript without having to scrape send invocations and
		// merge by timestamp. The TUI's bidirectional view already
		// renders both directions natively; this brings parity to the
		// listen-on-the-CLI use case.
		//
		// IMPORTANT: dir="out" means "WriteFrame returned without
		// error" — i.e. the framed payload was handed to the
		// underlying skywire conn. It does NOT mean the peer's
		// chat-app received or processed the message. Peer-app
		// receipt-ack is a deferred protocol feature (msg-id +
		// chat-ack envelope frame type); consumers should not treat
		// dir:out as delivery confirmation.
		//
		// The "sender" field carries the RECIPIENT'S PK here (per-peer
		// thread routing on the consumer's side stays keyed by the
		// remote PK regardless of direction). dir distinguishes
		// directionality (string, not bool, for extensibility — future
		// relay/group flows can emit "relay" / "group-in" /
		// "group-out" without a wire-schema bump).
		mirrorMsg, mErr := json.Marshal(map[string]interface{}{
			"sender":  pk.Hex(),
			"message": data["message"],
			"network": string(netType),
			"dir":     "out",
		})
		if mErr == nil {
			hub.broadcast(string(mirrorMsg))
		}
	}
}

// sseKeepaliveInterval is how often sseHandler writes a `: ping`
// comment line to keep the connection warm. SSE per the spec ignores
// any line starting with `:` so this is invisible to clients. The
// interval is well below the http.Server.WriteTimeout we set on the
// listener so write activity never goes idle long enough to trigger
// a deadline-based close — and any reverse proxy in front of skychat
// (Caddy/nginx) also sees a steady stream and won't time out.
const sseKeepaliveInterval = 15 * time.Second

func sseHandler(w http.ResponseWriter, req *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusBadRequest)
		return
	}

	// Disable WriteTimeout for this request. Long-lived SSE streams
	// are fundamentally incompatible with a per-conn write deadline
	// — an idle subscriber would see the server tear down the conn
	// after WriteTimeout and the client surfaces it as `unexpected
	// EOF`. Clearing the deadline keeps the conn open until either
	// the client closes it or req.Context().Done() fires on shutdown.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		// Older Go versions or wrapped writers may not support it;
		// we can still serve — just slightly more aggressive close
		// behavior if the operator runs an old server. Debug-log
		// rather than failing the connection.
		appCl.Log().Debugf("SSE SetWriteDeadline: %v", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering

	ch, unsubscribe := hub.subscribe()
	defer unsubscribe()

	// Seed the new SSE client with recent history if persistence is enabled.
	if historyStore != nil && persistSeedCount > 0 {
		recent, err := historyStore.ListRecent(persistSeedCount)
		if err != nil {
			appCl.Log().Debugf("SSE seed list failed: %v", err)
		} else {
			for _, m := range recent {
				sender := m.From
				if m.Outgoing {
					sender = "self"
				}
				b, _ := json.Marshal(map[string]string{ //nolint:errcheck,gosec
					"sender":  sender,
					"message": m.Text,
					"peer":    m.Peer,
					"ts":      m.Timestamp.Format(time.RFC3339),
					"history": "true",
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b) //nolint:errcheck,gosec
			}
			f.Flush()
		}
	}

	// Emit an initial keepalive comment immediately so the client
	// gets confirmation the stream is open even before any real
	// message arrives. Browsers (EventSource) and our CLI listen
	// both treat lines beginning with `:` as no-ops.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		appCl.Log().Debugf("SSE initial keepalive write failed: %v", err)
		return
	}
	f.Flush()

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				// Client gone (write to a closed conn) — exit so the
				// hub deregisters this subscriber and stops buffering
				// messages it can't deliver.
				appCl.Log().Debugf("SSE write failed, dropping subscriber: %v", err)
				return
			}
			f.Flush()

		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				appCl.Log().Debugf("SSE keepalive write failed: %v", err)
				return
			}
			f.Flush()

		case <-req.Context().Done():
			appCl.Log().Debug("SSE connection was closed.")
			return
		}
	}
}

// historyHandler returns JSON history. Query params:
//
//	peer=<pk>    — filter to a specific peer
//	limit=<int>  — max messages to return (default 100, max 1000)
func historyHandler(w http.ResponseWriter, req *http.Request) {
	if historyStore == nil {
		http.Error(w, "persistence not enabled", http.StatusServiceUnavailable)
		return
	}
	peer := req.URL.Query().Get("peer")
	limit := 100
	if v := req.URL.Query().Get("limit"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var msgs []history.Message
	var err error
	if peer != "" {
		msgs, err = historyStore.ListByPeer(peer, limit)
	} else {
		msgs, err = historyStore.ListRecent(limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs) //nolint:errcheck,gosec
}

func historyPeersHandler(w http.ResponseWriter, _ *http.Request) {
	if historyStore == nil {
		http.Error(w, "persistence not enabled", http.StatusServiceUnavailable)
		return
	}
	peers, err := historyStore.Peers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(peers) //nolint:errcheck,gosec
}

// statusHandler returns a snapshot of the chat-app's runtime health
// for operator probes. Replaces the chain of `docker exec + ss -tlnp
// + curl /sse` an operator would otherwise need to verify the app
// is up. JSON shape is stable — added fields are backward-compatible.
func statusHandler(w http.ResponseWriter, _ *http.Request) {
	connsMu.Lock()
	connCount := len(conns)
	peers := make([]string, 0, connCount)
	for pk := range conns {
		peers = append(peers, pk.Hex())
	}
	connsMu.Unlock()

	var subscriberCount int
	if hub != nil {
		subscriberCount = hub.clientCount()
	}

	var visorPK string
	var visorPKErr string
	if appCl != nil {
		visorPK = appCl.Config().VisorPK.Hex()
	} else {
		visorPKErr = "app client not initialized"
	}

	status := map[string]interface{}{
		"visor_pk":            visorPK,
		"sse_subscribers":     subscriberCount,
		"active_peer_conns":   connCount,
		"peers":               peers,
		"persistence_enabled": historyStore != nil,
		"pairing_enabled":     pairEnable,
	}
	if visorPKErr != "" {
		status["error"] = visorPKErr
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status) //nolint:errcheck,gosec
}

// persistMessage stores a message in the history backend if persistence is
// enabled. Errors are logged at debug level; ephemeral delivery is never
// blocked by persistence failure.
func persistMessage(msg history.Message) {
	if historyStore == nil {
		return
	}
	if err := historyStore.Append(msg); err != nil {
		switch {
		case errors.Is(err, history.ErrRateLimited),
			errors.Is(err, history.ErrTooLarge),
			errors.Is(err, history.ErrStorageFull),
			errors.Is(err, history.ErrNotWhitelisted):
			appCl.Log().Debugf("history: dropped %s (%v)", msg.Peer, err)
		default:
			appLog("history: backend error: %v", err)
		}
	}
}

// openHistoryStore constructs the bolt history store from CLI flags.
func openHistoryStore() error {
	dbPath := persistDBPath
	if dbPath == "" {
		workDir := appCl.Config().ProcWorkDir
		if workDir == "" {
			workDir = skyenv.LocalPath
		}
		dbPath = filepath.Join(workDir, "skychat-history.db")
	}

	limits := history.Limits{
		MaxMessageSize:    persistMaxMsgSize,
		PerPeerRatePerMin: persistPerPeerRate,
		PerPeerCap:        persistPerPeerCap,
		TotalCapBytes:     int64(persistTotalCapMB) * 1024 * 1024,
		TTL:               time.Duration(persistTTLDays) * 24 * time.Hour,
	}
	if persistWhitelistFile != "" {
		wl, err := loadWhitelist(persistWhitelistFile)
		if err != nil {
			return fmt.Errorf("load whitelist: %w", err)
		}
		limits.WhitelistOnly = true
		limits.Whitelist = wl
	}

	s, err := history.NewBoltStore(dbPath, limits)
	if err != nil {
		return err
	}
	historyStore = s
	appLog("Persistence enabled: db=%s cap=%dMB per-peer=%d ttl=%dd whitelist=%v",
		dbPath, persistTotalCapMB, persistPerPeerCap, persistTTLDays, limits.WhitelistOnly)
	return nil
}

// loadWhitelist reads a file with one peer PK hex per line (ignoring blanks
// and lines starting with #).
func loadWhitelist(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	wl := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		wl[line] = true
	}
	return wl, nil
}

func getFileSystem() http.FileSystem {
	fsys, err := fs.Sub(embededFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(fsys)
}

func handleIPCSignal(client *ipc.Client) {
	time.Sleep(5 * time.Second)
	if client == nil {
		appLog("Unable to create IPC Client: server is non-existent")
		return
	}
	for {
		m, err := client.Read()
		if err != nil {
			appLog("%s IPC received error: %v", skyenv.SkychatName, err)
		}

		if m != nil {
			if m.MsgType == skyenv.IPCShutdownMessageType {
				appLog("Stopping %s via IPC", skyenv.SkychatName)
				break
			}
		}

	}
	client.Close()
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		appCl.Log().Errorf("Failed to set status %v: %v", status, err)
	}
}

func setAppError(appCl *app.Client, appErr error) {
	if err := appCl.SetError(appErr.Error()); err != nil {
		appCl.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

func setAppPort(appCl *app.Client, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		appCl.Log().Errorf("Failed to set port %v: %v", port, err)
	}
}
