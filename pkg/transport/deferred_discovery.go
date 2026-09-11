// Package transport pkg/transport/deferred_discovery.go c2-net-transport
//
// DeferredDiscoveryClient lets a visor finish booting while the transport
// discovery is unreachable.
//
// Constructing the real TPD client performs network I/O — an httpauth nonce
// fetch — so it fails outright when the TPD cannot be reached. The visor used
// to retry that construction, unbounded, on the boot path. The transport
// module gates the visor's RPC server, its launcher (and therefore dmsgpty,
// the hypervisor RPC and every app listener) and its router, so a visor that
// could not reach the TPD never finished starting: alive, holding dmsg
// sessions, answering nothing, and unmanageable for as long as the TPD stayed
// away.
//
// That is a cascade, not just an outage. The deployment services are reached
// over dmsg, so a visor co-located with them cannot register its transports
// until they answer — and if the TPD is itself behind a visor that is stuck in
// the same wait, neither ever proceeds. Observed in production: one host's
// visor wedged, which took the transport discovery with it, and every visor
// that restarted afterwards wedged the same way on the missing TPD, spreading
// with the rollout.
//
// A visor's transports are useful before they are registered, and registration
// is already retried on a timer, so the TPD is not a boot prerequisite. This
// wrapper makes that explicit: boot proceeds with a client whose calls fail
// cleanly until the real one connects in the background, after which every
// call is forwarded to it.
package transport

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// ErrDiscoveryNotReady is returned by a DeferredDiscoveryClient that has not
// connected yet. It is deliberately distinct from a transport error: the call
// did not fail on the wire, it never left.
var ErrDiscoveryNotReady = errors.New("transport discovery not connected yet")

// DiscoveryConnectFunc builds the real discovery client. It performs network
// I/O and returns an error while the discovery is unreachable.
type DiscoveryConnectFunc func(ctx context.Context) (DiscoveryClient, error)

// DeferredDiscoveryClient is a DiscoveryClient that forwards to the real one
// once it connects, and fails fast before that.
type DeferredDiscoveryClient struct {
	mu    sync.RWMutex
	inner DiscoveryClient

	log     *logging.Logger
	connect DiscoveryConnectFunc
	ready   chan struct{}
	once    sync.Once
}

// NewDeferredDiscoveryClient returns a client that is not connected yet and a
// goroutine that keeps trying to connect until ctx ends. interval bounds how
// often connection is attempted.
func NewDeferredDiscoveryClient(ctx context.Context, log *logging.Logger, interval time.Duration, connect DiscoveryConnectFunc) *DeferredDiscoveryClient {
	d := &DeferredDiscoveryClient{
		log:     log,
		connect: connect,
		ready:   make(chan struct{}),
	}
	go d.connectLoop(ctx, interval)
	return d
}

// Ready is closed once the real client is connected.
func (d *DeferredDiscoveryClient) Ready() <-chan struct{} { return d.ready }

// Connected reports whether the real client is in place.
func (d *DeferredDiscoveryClient) Connected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.inner != nil
}

func (d *DeferredDiscoveryClient) connectLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		client, err := d.connect(ctx)
		if err == nil {
			d.mu.Lock()
			d.inner = client
			d.mu.Unlock()
			d.once.Do(func() { close(d.ready) })
			d.log.Info("Transport discovery connected; transports will register from here on")
			return
		}
		if ctx.Err() != nil {
			return
		}
		d.log.WithError(err).Debug("Transport discovery still unreachable; the visor runs without it and keeps retrying")
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// client returns the real client, or ErrDiscoveryNotReady.
func (d *DeferredDiscoveryClient) client() (DiscoveryClient, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.inner == nil {
		return nil, ErrDiscoveryNotReady
	}
	return d.inner, nil
}

// RegisterTransports implements DiscoveryClient.
func (d *DeferredDiscoveryClient) RegisterTransports(ctx context.Context, entries ...*SignedEntry) error {
	c, err := d.client()
	if err != nil {
		return err
	}
	return c.RegisterTransports(ctx, entries...)
}

// RegisterTransportsV3 implements DiscoveryClient.
func (d *DeferredDiscoveryClient) RegisterTransportsV3(ctx context.Context, version string, entries ...*Entry) error {
	c, err := d.client()
	if err != nil {
		return err
	}
	return c.RegisterTransportsV3(ctx, version, entries...)
}

// GetTransportByID implements DiscoveryClient.
func (d *DeferredDiscoveryClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*Entry, error) {
	c, err := d.client()
	if err != nil {
		return nil, err
	}
	return c.GetTransportByID(ctx, id)
}

// GetTransportsByEdge implements DiscoveryClient.
func (d *DeferredDiscoveryClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*Entry, error) {
	c, err := d.client()
	if err != nil {
		return nil, err
	}
	return c.GetTransportsByEdge(ctx, pk)
}

// GetAllTransports implements DiscoveryClient.
func (d *DeferredDiscoveryClient) GetAllTransports(ctx context.Context) ([]*Entry, error) {
	c, err := d.client()
	if err != nil {
		return nil, err
	}
	return c.GetAllTransports(ctx)
}

// GetTransportStats implements DiscoveryClient.
func (d *DeferredDiscoveryClient) GetTransportStats(ctx context.Context, pk cipher.PubKey) (*TransportStats, error) {
	c, err := d.client()
	if err != nil {
		return nil, err
	}
	return c.GetTransportStats(ctx, pk)
}

// GetAllTransportsStats implements DiscoveryClient.
func (d *DeferredDiscoveryClient) GetAllTransportsStats(ctx context.Context) (*NetworkTransportStats, error) {
	c, err := d.client()
	if err != nil {
		return nil, err
	}
	return c.GetAllTransportsStats(ctx)
}

// GetAllTransportsPerKeyStats implements DiscoveryClient.
func (d *DeferredDiscoveryClient) GetAllTransportsPerKeyStats(ctx context.Context) (PerKeyStats, error) {
	c, err := d.client()
	if err != nil {
		return nil, err
	}
	return c.GetAllTransportsPerKeyStats(ctx)
}

// DeleteTransport implements DiscoveryClient.
func (d *DeferredDiscoveryClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	c, err := d.client()
	if err != nil {
		return err
	}
	return c.DeleteTransport(ctx, id)
}

// DeleteTransports implements DiscoveryClient.
func (d *DeferredDiscoveryClient) DeleteTransports(ctx context.Context, ids []uuid.UUID) (int, error) {
	c, err := d.client()
	if err != nil {
		return 0, err
	}
	return c.DeleteTransports(ctx, ids)
}
