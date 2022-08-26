// Package appevent broadcaster.go
package appevent

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// Broadcaster combines multiple RPCClients (which connects to the RPCGateway of the apps).
// It is responsible for broadcasting events to apps (if the app is subscribed to the event type).
type Broadcaster struct {
	timeout time.Duration

	log     logrus.FieldLogger
	clients map[RPCClient]chan error
	closed  bool
	mx      sync.Mutex
}

// NewBroadcaster instantiates a Broadcaster.
func NewBroadcaster(log logrus.FieldLogger, timeout time.Duration) *Broadcaster {
	if log == nil {
		log = logging.MustGetLogger("event_broadcaster")
	}
	return &Broadcaster{
		timeout: timeout,
		log:     log,
		clients: make(map[RPCClient]chan error),
		closed:  false,
	}
}

// AddClient adds a RPCClient.
func (mc *Broadcaster) AddClient(c RPCClient) {
	mc.mx.Lock()
	if !mc.closed {
		mc.clients[c] = make(chan error, 1)
	}
	mc.mx.Unlock()
}

// Broadcast broadcasts an event to all subscribed channels of all rpc gateways.
func (mc *Broadcaster) Broadcast(ctx context.Context, e *Event) error {
	if mc.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.Now().Add(mc.timeout))
		defer cancel()
	}

	mc.mx.Lock()
	defer mc.mx.Unlock()

	if mc.closed {
		return ErrSubscriptionsClosed
	}

	if len(mc.clients) == 0 {
		return nil
	}

	// Notify all clients of event (if client is subscribed to the event type).
	for client, errCh := range mc.clients {
		go notifyClient(ctx, e, client, errCh)
	}

	// Delete inactive clients and associated error channels.
	for client, errCh := range mc.clients {
		if err := <-errCh; err != nil {
			if err.Error() != "connection is shut down" {
				mc.log.
					WithError(err).
					WithField("close_error", client.Close()).
					WithField("hello", client.Hello().String()).
					Warn("Events RPC client closed due to error.")
			}

			delete(mc.clients, client)
			close(errCh)
		}
	}

	return nil
}

// notifyClient notifies a client of a given event if client is subscribed to the event type of the event.
func notifyClient(ctx context.Context, e *Event, client RPCClient, errCh chan error) {
	var err error
	if client.Hello().AllowsEventType(e.Type) {
		err = client.Notify(ctx, e)
	}
	errCh <- err
}

// Close implements io.Closer
func (mc *Broadcaster) Close() error {
	mc.mx.Lock()
	defer mc.mx.Unlock()

	if mc.closed {
		return ErrSubscriptionsClosed
	}
	mc.closed = true

	for c, errCh := range mc.clients {
		close(errCh)
		delete(mc.clients, c)
	}
	return nil
}
