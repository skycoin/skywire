// Package visor pkg/visor/embedded_wisp.go c3-vis-core
//
// EmbeddedWisp is the in-process Wisp server: the same protocol
// `skywire cli wisp serve` speaks, bound to the page's virtual loopback
// instead of an HTTP port.
//
// Why in-process and why vnet. A browser-side Linux guest gets its network by
// asking a Wisp backend to open connections by name, and the backends such a
// page normally uses are one central WebSocket somewhere. A visor running in
// the same tab can be that backend instead, which puts the guest's traffic on
// a route to a skywire exit. Two things force the shape:
//
//   - websocket.Accept is //go:build !js, so a WebSocket server cannot exist
//     in a tab at all;
//   - a service worker cannot intercept ws:// or wss://, so a page-served
//     WebSocket endpoint could not be reached even if one existed.
//
// So the session is framed over a plain conn (wisp.Server.ServeConn) and
// carried on a vnet port, which page JS reaches with vnet.dial(port). On a
// native visor bottle's vnet is net, so the same code binds ordinary loopback
// and the session is reachable with `wisp.DialConn`.
//
// Egress is the visor's own skysocks-client. Reaching it needs bottle's vnet
// as the forward dialer: in a tab the proxy's port lives in the page's port
// table, which net.Dial knows nothing about.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/0magnet/bottle/vnet"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wisp"
)

// vnetForward reaches a virtual-loopback address. It is bottle's vnet behind
// the proxy.Dialer shape golang.org/x/net/proxy wants, and on a native visor
// vnet.DialTimeout is net.DialTimeout.
type vnetForward struct{}

// Dial implements proxy.Dialer.
func (vnetForward) Dial(network, addr string) (net.Conn, error) {
	return vnet.DialTimeout(network, addr, 10*time.Second)
}

// EmbeddedWisp holds the runtime state of the in-process Wisp server. Safe for
// concurrent Start/Stop.
type EmbeddedWisp struct {
	cfg *visorconfig.WispConfig
	log *logging.Logger

	mu     sync.Mutex
	lis    net.Listener
	cancel context.CancelFunc
}

// newEmbeddedWisp builds the runtime without starting it.
func newEmbeddedWisp(cfg *visorconfig.WispConfig, log *logging.Logger) *EmbeddedWisp {
	return &EmbeddedWisp{cfg: cfg, log: log}
}

// port is the virtual-loopback port to bind.
func (w *EmbeddedWisp) port() uint {
	if w.cfg.Port != 0 {
		return w.cfg.Port
	}
	return visorconfig.DefaultWispPort
}

// upstream is the SOCKS5 proxy the streams leave through.
func (w *EmbeddedWisp) upstream() string {
	if w.cfg.UpstreamSOCKS != "" {
		return w.cfg.UpstreamSOCKS
	}
	// SkysocksClientAddr is ":1080" — the port the proxy app's SOCKS5
	// listener binds, not its dmsg port.
	return "127.0.0.1" + skyenv.SkysocksClientAddr
}

// Addr reports the address the server is bound to, or "" when it is stopped.
func (w *EmbeddedWisp) Addr() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lis == nil {
		return ""
	}
	return w.lis.Addr().String()
}

// Running reports whether the server is bound.
func (w *EmbeddedWisp) Running() bool { return w.Addr() != "" }

// Start binds the virtual-loopback port and serves sessions until Stop.
func (w *EmbeddedWisp) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lis != nil {
		return errors.New("wisp: already started")
	}

	egress, err := wisp.NewSocksEgressThrough(w.upstream(), vnetForward{})
	if err != nil {
		return fmt.Errorf("wisp egress: %w", err)
	}
	srv, err := wisp.NewServer(wisp.Config{
		Egress: egress,
		Buffer: w.cfg.Buffer,
		Log:    w.log,
	})
	if err != nil {
		return fmt.Errorf("wisp server: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", w.port())
	lis, err := vnet.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("wisp listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w.lis, w.cancel = lis, cancel

	go w.serve(ctx, srv, lis)
	w.log.Infof("Embedded wisp server on %s, egress %s", addr, egress.Describe())
	return nil
}

// serve accepts conns and runs one session on each.
func (w *EmbeddedWisp) serve(ctx context.Context, srv *wisp.Server, lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
			default:
				w.log.WithError(err).Debug("wisp accept ended")
			}
			return
		}
		// One session per conn, for as long as the page keeps it. A
		// page that reloads drops its conn and takes its streams with
		// it, which is the lifetime a guest NIC wants.
		go srv.ServeConn(ctx, conn)
	}
}

// Stop releases the port. It is safe to call when already stopped.
func (w *EmbeddedWisp) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lis == nil {
		return nil
	}
	w.cancel()
	err := w.lis.Close()
	w.lis, w.cancel = nil, nil
	w.log.Info("Embedded wisp server stopped")
	return err
}

// initEmbeddedWisp constructs the runtime so it can be started and stopped
// without a visor restart, then auto-starts it when Enable=true. Construction
// is unconditional within "config section present", Start is conditional —
// mirroring initEmbeddedSkymailBridge.
func initEmbeddedWisp(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || v.conf.Wisp == nil {
		log.Debug("wisp section absent; not constructing the server")
		return nil
	}

	runtime := newEmbeddedWisp(v.conf.Wisp, log)
	v.initLock.Lock()
	v.embeddedWisp = runtime
	v.initLock.Unlock()

	if !v.conf.Wisp.Enable {
		log.Info("Embedded wisp server constructed but not started (enable=false)")
		return nil
	}
	if err := runtime.Start(); err != nil {
		// Not fatal: a port already claimed by another wasm instance in
		// the same page is a normal collision, not a broken visor.
		log.WithError(err).Warn("failed to auto-start the embedded wisp server")
	}
	return nil
}

// compile-time proof the forward dialer satisfies what the egress wants.
var _ proxy.Dialer = vnetForward{}
