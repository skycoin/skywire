// Package commands — cmd/apps/skychat/commands/tcp_direct.go:
// chat-app's noise-TCP entry points, the reliability-floor transport
// described in #2706. Same noise-XK primitive dmsgpty uses, applied
// to chat frames so agents can reach each other when dmsg-disc /
// skynet / visor itself is misbehaving.
//
// Two halves:
//
//  1. --tcp-listen :PORT — accept noise-XK on TCP, wrap the
//     resulting conn so chat-app's existing handleConn loop (which
//     expects appnet.Addr on RemoteAddr) can consume it unchanged.
//
//  2. --tcp-peer tcp://<pk>@host:port (repeatable) — maintain a
//     persistent outbound dial per listed peer. Reconnect with
//     exponential backoff. Lets agents behind NAT reach
//     public-IP peers and HAVE A BIDIRECTIONAL channel: once the
//     handshake completes, the listener side can write back over
//     the same socket. Operator only port-forwards on the public
//     side; NAT side runs --tcp-peer only.
//
// Identity comes from the chat-app's --sk / -c / DMSGCURL_SK
// resolution chain (skychat.go owns those flags). Whitelist (the
// chat-app's existing --tcp-whitelist set) gates incoming peers
// the same way dmsgpty's whitelist does — empty list rejects all,
// which is fail-safe.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire/tcpnoise"
)

// osGetenv exists only to make the resolveTCPIdentity test path
// hookable later without pulling in os everywhere. Today it's a
// pass-through to os.Getenv.
func osGetenv(k string) string { return os.Getenv(k) }

// readSKFromConfig reads the "sk" field from a JSON file in
// skywire.json's shape. The visor's config has many other fields;
// we tolerate them and only consume sk. Returns a clear error if
// the file is missing, isn't JSON, or has no sk field.
func readSKFromConfig(path string) (cipher.SecKey, error) {
	var zero cipher.SecKey
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return zero, fmt.Errorf("read %s: %w", path, err)
	}
	var conf struct {
		SK string `json:"sk"`
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		return zero, fmt.Errorf("parse %s: %w", path, err)
	}
	if conf.SK == "" {
		return zero, fmt.Errorf("%s has no sk field", path)
	}
	var sk cipher.SecKey
	if err := sk.Set(conf.SK); err != nil {
		return zero, fmt.Errorf("%s sk: %w", path, err)
	}
	return sk, nil
}

// tcpReconnectMin / tcpReconnectMax bound the outbound-dial backoff.
// Mirrors the reconnect cadence the SSE listener uses; long enough
// that a permanently-unreachable peer doesn't burn CPU.
const (
	tcpReconnectMin = 1 * time.Second
	tcpReconnectMax = 60 * time.Second
)

// Flag-backed vars owned by the chat-app's flag registration in
// skychat.go. Default zero values mean "TCP-direct disabled" —
// the chat-app only spawns the TCP loops when an operator opts
// in. Keeping these here (rather than in skychat.go's catch-all
// var block) keeps the tcp-direct surface visibly separable so
// future #2706 work that promotes the pattern across other apps
// can lift this file into a shared package wholesale.
var (
	tcpListen     string   // --tcp-listen :PORT  (empty disables)
	tcpPeers      []string // --tcp-peer  tcp://<pk>@host:port  (repeatable)
	tcpWhitelist  string   // --tcp-whitelist <pk>,<pk>  (REQUIRED if --tcp-listen)
	tcpSKFlag     string   // --sk <hex>  (overrides config / env / ephemeral)
	tcpConfigPath string   // -c / --config <path>  (reads SK from skywire.json)
)

// tcpDirectConn wraps a tcpnoise.Conn so RemoteAddr() returns an
// appnet.Addr — chat-app's existing handleConn does a type assertion
// to appnet.Addr on the addr; satisfying that contract here lets us
// reuse the entire frame-handler + counter + persistence path
// without per-transport branches.
type tcpDirectConn struct {
	net.Conn // tcpnoise.Conn — passes through LocalAddr/Close/etc
	rPK      cipher.PubKey
}

// RemoteAddr returns the noise-learned PK wrapped in appnet.Addr
// shape so handleConn's `conn.RemoteAddr().(appnet.Addr)` succeeds.
// Net=TypeTCPDirect surfaces in SSE events as the network field so
// operators can see which transport carried each inbound message.
func (c *tcpDirectConn) RemoteAddr() net.Addr {
	return appnet.Addr{Net: appnet.TypeTCPDirect, PubKey: c.rPK}
}

// parseTCPPeerSpec turns "tcp://<66-hex-pk>@host:port" into its
// components. Same shape as dmsgpty's --via tcp://; centralized so
// errors are consistent. Returns ErrTCPPeerBadShape on any
// formatting failure with the offending input echoed back.
func parseTCPPeerSpec(spec string) (cipher.PubKey, string, error) {
	const prefix = "tcp://"
	if !strings.HasPrefix(spec, prefix) {
		return cipher.PubKey{}, "", fmt.Errorf("tcp-peer: must start with %q: %s", prefix, spec)
	}
	rest := strings.TrimPrefix(spec, prefix)
	at := strings.IndexByte(rest, '@')
	if at <= 0 || at == len(rest)-1 {
		return cipher.PubKey{}, "", fmt.Errorf("tcp-peer: missing @ separator in %q", spec)
	}
	pkHex := rest[:at]
	addr := rest[at+1:]
	var pk cipher.PubKey
	if err := pk.Set(pkHex); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("tcp-peer: invalid pk %q: %w", pkHex, err)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("tcp-peer: invalid host:port %q: %w", addr, err)
	}
	return pk, addr, nil
}

// runTCPListen spawns the accept loop on tcpListenAddr. Each
// accepted conn runs the noise XK handshake (responder side),
// gets its peer-PK authorized against the chat-app's whitelist
// (when set), and then enters the standard handleConn path —
// identical to the dmsg / skynet accept paths in skychat.go.
//
// localPK / localSK is the chat-app's identity; tcpWhitelist (set
// when len > 0) is the per-PK accept list. Empty whitelist rejects
// all incoming peers — fail-safe for misconfiguration.
//
// Returns when the listener errors or ctx is canceled.
func runTCPListen(
	ctx context.Context,
	listenAddr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	whitelist map[cipher.PubKey]struct{},
) error {
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("tcp-listen %s: %w", listenAddr, err)
	}
	appLog("skychat: tcp-direct listening on %s", listenAddr)
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	for {
		raw, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			appLog("skychat: tcp-direct Accept: %v", err)
			return err
		}
		go acceptTCPConn(raw, localPK, localSK, whitelist)
	}
}

// acceptTCPConn runs the noise handshake on a single accepted TCP
// conn, authorizes the learned PK against the whitelist, registers
// the conn in the chat-app's conns map keyed by PK, and kicks off
// handleConn. Mirrors the dmsg-accept flow in skychat.go's listen
// loop — kept here so the tcp-specific bookkeeping (raw close on
// reject, the wrapper for appnet.Addr-style RemoteAddr) doesn't
// pollute the dmsg path.
func acceptTCPConn(
	raw net.Conn,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	whitelist map[cipher.PubKey]struct{},
) {
	wrapped, rPK, err := tcpnoise.Accept(raw, localPK, localSK)
	if err != nil {
		appLog("skychat: tcp-direct handshake failed from %s: %v", raw.RemoteAddr(), err)
		return
	}
	// Whitelist gate. Empty whitelist = reject all (the chat-app
	// must be explicitly configured to accept). This matches
	// dmsgpty's host_tcp.go behavior.
	if len(whitelist) == 0 {
		appLog("skychat: tcp-direct rejecting %s: no whitelist configured", rPK)
		_ = wrapped.Close() //nolint:errcheck
		return
	}
	if _, ok := whitelist[rPK]; !ok {
		appLog("skychat: tcp-direct rejecting %s: not on whitelist", rPK)
		_ = wrapped.Close() //nolint:errcheck
		return
	}

	tdc := &tcpDirectConn{Conn: wrapped, rPK: rPK}
	fc := newFramedConn(tdc)
	connsMu.Lock()
	conns[rPK] = fc
	connsMu.Unlock()
	appLog("skychat: tcp-direct conn accepted from %s", rPK)
	handleConn(fc)
}

// runTCPPeerDialers spawns one persistent-dial goroutine per
// --tcp-peer spec. Each goroutine maintains a single outbound
// conn, redials on disconnect with exponential backoff. Existing
// conn slot in the conns map is left to the natural handleConn
// teardown path — we don't pre-evict because a slow peer might
// still be live just slow.
//
// Returns immediately; goroutines run for the chat-app's lifetime.
func runTCPPeerDialers(
	ctx context.Context,
	peers []string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
) {
	for _, spec := range peers {
		pk, addr, err := parseTCPPeerSpec(spec)
		if err != nil {
			appLog("skychat: tcp-peer skipping bad spec %q: %v", spec, err)
			continue
		}
		go peerDialLoop(ctx, pk, addr, localPK, localSK)
	}
}

// peerDialLoop is the persistent-outbound state machine for a single
// --tcp-peer. Dial → handshake → register → handleConn (blocks
// until peer disconnect) → backoff → repeat. Backoff resets on
// successful handshake so a flapping peer can recover quickly once
// it stabilizes; long-down peers ride the cap.
func peerDialLoop(
	ctx context.Context,
	rPK cipher.PubKey,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
) {
	backoff := tcpReconnectMin
	for {
		if ctx.Err() != nil {
			return
		}
		dialCtx, cancel := context.WithTimeout(ctx, tcpnoise.DialTimeout+tcpnoise.HandshakeTimeout)
		conn, err := tcpnoise.Dial(dialCtx, addr, localPK, localSK, rPK)
		cancel()
		if err != nil {
			appLog("skychat: tcp-peer dial %s@%s failed: %v (backoff %s)", rPK, addr, err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > tcpReconnectMax {
				backoff = tcpReconnectMax
			}
			continue
		}
		// Successful dial — reset backoff so any subsequent disconnect
		// retries quickly before extending the gap.
		backoff = tcpReconnectMin

		tdc := &tcpDirectConn{Conn: conn, rPK: rPK}
		fc := newFramedConn(tdc)
		connsMu.Lock()
		// If a prior conn (from accept side OR a previous loop iter)
		// is still registered, supersede it. The old framedConn's
		// handleConn will exit naturally on its next ReadFrame.
		conns[rPK] = fc
		connsMu.Unlock()
		appLog("skychat: tcp-peer connected to %s@%s", rPK, addr)
		// handleConn blocks until the peer disconnects (ReadFrame
		// error). On return we'll loop and redial.
		handleConn(fc)
		appLog("skychat: tcp-peer disconnected from %s@%s", rPK, addr)
	}
}

// resolveTCPIdentity returns the SK + derived PK used as this
// chat-app's identity for noise XK. Resolution order, first hit
// wins:
//
//  1. --sk SECKEY                     (explicit override)
//  2. DMSGCURL_SK env                 (operator convention)
//  3. -c / --config PATH              (read SK field from skywire.json)
//  4. fail loudly                     (TCP-direct REQUIRES a stable
//     identity; ephemeral noise PKs
//     defeat the trust model)
//
// Called once at chat-app startup from startTCPDirect; SK material
// is not held beyond what tcpnoise.Dial / Accept need on the call
// stack.
func resolveTCPIdentity() (cipher.PubKey, cipher.SecKey, string, error) {
	var zero cipher.SecKey
	if tcpSKFlag != "" {
		var sk cipher.SecKey
		if err := sk.Set(tcpSKFlag); err != nil {
			return cipher.PubKey{}, zero, "", fmt.Errorf("--sk: %w", err)
		}
		pk, err := sk.PubKey()
		if err != nil {
			return cipher.PubKey{}, zero, "", fmt.Errorf("--sk: derive pk: %w", err)
		}
		return pk, sk, "flag", nil
	}
	if env := osGetenv("DMSGCURL_SK"); env != "" {
		var sk cipher.SecKey
		if err := sk.Set(env); err == nil {
			pk, err := sk.PubKey()
			if err == nil {
				return pk, sk, "env", nil
			}
		}
	}
	if tcpConfigPath != "" {
		sk, err := readSKFromConfig(tcpConfigPath)
		if err != nil {
			return cipher.PubKey{}, zero, "", fmt.Errorf("-c %s: %w", tcpConfigPath, err)
		}
		pk, err := sk.PubKey()
		if err != nil {
			return cipher.PubKey{}, zero, "", fmt.Errorf("-c %s: derive pk: %w", tcpConfigPath, err)
		}
		return pk, sk, "config", nil
	}
	return cipher.PubKey{}, zero, "", errors.New(
		"TCP-direct enabled but no identity configured " +
			"(set --sk, DMSGCURL_SK env, or -c /path/to/skywire.json)")
}

// startTCPDirect is the chat-app's TCP-direct entry point. Wired
// from RunSkychat AFTER the dmsg/skynet listenLoops are spawned, so
// the conns map is initialized. Spawns the accept loop (when
// --tcp-listen set) and the per-peer dial loops (when --tcp-peer set)
// using the resolved identity. Returns early with nil when neither
// flag is set — TCP-direct is fully opt-in.
func startTCPDirect(ctx context.Context) error {
	if tcpListen == "" && len(tcpPeers) == 0 {
		return nil
	}
	pk, sk, source, err := resolveTCPIdentity()
	if err != nil {
		return fmt.Errorf("tcp-direct: %w", err)
	}
	appLog("skychat: tcp-direct identity pk=%s (source: %s)", pk, source)

	whitelist := tcpWhitelistSet(tcpWhitelist)
	if tcpListen != "" {
		if len(whitelist) == 0 {
			appLog("skychat: tcp-direct WARNING --tcp-listen set without --tcp-whitelist; will reject all incoming peers")
		}
		go func() {
			if err := runTCPListen(ctx, tcpListen, pk, sk, whitelist); err != nil {
				appLog("skychat: tcp-direct listener exited: %v", err)
			}
		}()
	}
	if len(tcpPeers) > 0 {
		runTCPPeerDialers(ctx, tcpPeers, pk, sk)
	}
	return nil
}

// tcpWhitelistSet builds a PK-set from a comma-separated string of
// hex PKs. Whitespace-tolerant; bad entries logged and skipped.
// Public so the chat-app's flag wiring can construct the set once
// and pass it to runTCPListen.
func tcpWhitelistSet(commaSep string) map[cipher.PubKey]struct{} {
	out := make(map[cipher.PubKey]struct{})
	if commaSep == "" {
		return out
	}
	mu := sync.Mutex{}
	for _, raw := range strings.Split(commaSep, ",") {
		hex := strings.TrimSpace(raw)
		if hex == "" {
			continue
		}
		var pk cipher.PubKey
		if err := pk.Set(hex); err != nil {
			appLog("skychat: tcp-whitelist skipping invalid pk %q: %v", hex, err)
			continue
		}
		mu.Lock()
		out[pk] = struct{}{}
		mu.Unlock()
	}
	return out
}
