// Package visor pkg/visor/embedded_skymail_bridge.go
//
// EmbeddedSkymailBridge wraps the pkg/skymailbridge SMTP server so it
// can be hosted directly inside the visor process — the SMTP-side
// analog of EmbeddedDmsgWeb / EmbeddedSkynetWeb. The bridge accepts
// inbound SMTP from a co-located Postfix's transport_map, parses the
// recipient envelope, and dials peers over the visor's existing
// dmsg client. No separate app, no second dmsg identity.
//
// Lifecycle mirrors the resolvers exactly: idempotent Start/Stop,
// running flag, runtime toggle via RPC
// (SetEmbeddedProxyEnabled("bridge", …)). Standalone deployments
// (hosts without a full visor) should use cmd/smb instead.
package visor

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skymailbridge"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// defaultSkymailBridgeAddr is the listener used when SkymailBridgeConfig.Addr
// is empty. Matches the documented Postfix transport_map example.
const defaultSkymailBridgeAddr = "127.0.0.1:1025"

// EmbeddedSkymailBridge is the runtime state of the in-process SMTP
// bridge. Field layout intentionally mirrors EmbeddedDmsgWeb to keep
// the lifecycle code shape uniform.
type EmbeddedSkymailBridge struct {
	dmsgC *dmsg.Client
	cfg   *visorconfig.SkymailBridgeConfig
	log   *logging.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	parentCtx context.Context // captured so Start() without args works after construction
}

// newEmbeddedSkymailBridge is the constructor used by
// initEmbeddedSkymailBridge.
func newEmbeddedSkymailBridge(parentCtx context.Context, dmsgC *dmsg.Client, cfg *visorconfig.SkymailBridgeConfig, log *logging.Logger) *EmbeddedSkymailBridge {
	return &EmbeddedSkymailBridge{
		dmsgC:     dmsgC,
		cfg:       cfg,
		log:       log,
		parentCtx: parentCtx,
	}
}

// Start spawns the SMTP server goroutine. Idempotent: calling Start
// while already running is a no-op. Returns an error if prerequisites
// (dmsg client) are missing or the listener can't bind.
func (e *EmbeddedSkymailBridge) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	if e.dmsgC == nil {
		return fmt.Errorf("skymail-bridge: no dmsg client available")
	}
	addr := e.cfg.Addr
	if addr == "" {
		addr = defaultSkymailBridgeAddr
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("skymail-bridge: listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(e.parentCtx)
	e.cancel = cancel
	e.done = make(chan struct{})
	e.running = true

	go func() {
		defer close(e.done)
		e.serve(ctx, lis)
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()
	e.log.WithField("addr", addr).Info("Embedded skymail-bridge started")
	return nil
}

// Stop signals the server to shut down and blocks until the
// goroutine returns. Idempotent.
func (e *EmbeddedSkymailBridge) Stop() error {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return nil
	}
	cancel := e.cancel
	done := e.done
	e.mu.Unlock()

	cancel()
	<-done
	e.log.Info("Embedded skymail-bridge stopped")
	return nil
}

// IsRunning reports whether the SMTP server goroutine is alive.
func (e *EmbeddedSkymailBridge) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Addr returns the configured listener address (or the default when empty).
func (e *EmbeddedSkymailBridge) Addr() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.Addr != "" {
		return e.cfg.Addr
	}
	return defaultSkymailBridgeAddr
}

// Mode returns the configured envelope rewrite mode.
func (e *EmbeddedSkymailBridge) Mode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.Mode != "" {
		return e.cfg.Mode
	}
	return "b"
}

func (e *EmbeddedSkymailBridge) serve(ctx context.Context, lis net.Listener) {
	cfg := skymailbridge.Config{
		Suffix:     e.cfg.Suffix,
		Mode:       e.cfg.Mode,
		HeloName:   e.cfg.HeloName,
		RemotePort: e.cfg.RemotePort,
	}
	dialer := &visorDmsgDialer{c: e.dmsgC}
	if err := skymailbridge.Serve(ctx, lis, dialer, cfg, e.log); err != nil && err != context.Canceled {
		e.log.WithError(err).Warn("skymail-bridge runtime stopped")
	}
}

// visorDmsgDialer satisfies skymailbridge.Dialer by speaking directly
// to the peer via the visor's shared dmsg client. No second
// discovery connection, no second identity — the bridge reuses the
// visor's running session, same way embedded_dmsgweb does.
type visorDmsgDialer struct{ c *dmsg.Client }

func (d *visorDmsgDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	return d.c.Dial(ctx, dmsg.Addr{PK: peer, Port: port})
}

// initEmbeddedSkymailBridge constructs the runtime so it can be
// started and stopped via RPC, then auto-starts it when
// Enable=true. Mirrors initEmbeddedDmsgWeb: construction is
// unconditional (within "config section present + dmsg client
// available"), Start is conditional. This lets the UI flip Enable
// at runtime without a visor restart.
func initEmbeddedSkymailBridge(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || v.conf.SkymailBridge == nil {
		log.Debug("skymail_bridge section absent; not constructing bridge")
		return nil
	}
	if v.dmsgC == nil {
		log.Warn("skymail_bridge configured but dmsg client not available; skipping")
		return nil
	}

	// Wait for the visor's dmsg client to be ready before constructing
	// so any auto-start path has a usable session at hand.
	select {
	case <-v.dmsgC.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}

	runtime := newEmbeddedSkymailBridge(ctx, v.dmsgC, v.conf.SkymailBridge, log)
	v.initLock.Lock()
	v.embeddedSkymailBridge = runtime
	v.initLock.Unlock()

	if v.conf.SkymailBridge.Enable {
		if err := runtime.Start(); err != nil {
			log.WithError(err).Warn("failed to auto-start skymail-bridge")
		}
	} else {
		log.Info("Embedded skymail-bridge constructed but not started (enable=false); toggle via RPC")
	}
	return nil
}
