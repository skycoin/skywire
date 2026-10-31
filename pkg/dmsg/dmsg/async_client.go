// Package dmsg pkg/dmsg/dmsg/async_client.go c1-net-dmsg
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// AsyncClientConfig turns dmsg-async fetching on for a client: it takes
// what servers hold for it and hands each message to the handler for its
// port. Depositing works without it.
type AsyncClientConfig struct {
	// FetchInterval is how often the client looks, besides at start and on
	// each new server session. Zero takes AsyncDefaultFetchInterval.
	FetchInterval time.Duration
	// Replicas is how many servers a deposit goes to and a fetch reads.
	// Zero takes AsyncDefaultReplicas.
	Replicas int
}

// AsyncHandler takes a fetched message. A nil error acks it and the server
// deletes it; an error leaves it there for the next fetch.
type AsyncHandler func(AsyncMessage) error

type asyncClient struct {
	conf    AsyncClientConfig
	kick    chan struct{}
	fetchMu sync.Mutex

	mu       sync.Mutex
	handlers map[uint16]AsyncHandler
}

func asyncReplicas(n int) int {
	if n <= 0 {
		return AsyncDefaultReplicas
	}
	return n
}

// EnableAsync turns fetching on for a client built without Config.Async,
// as Config.Async would have. Call it before Serve; a second call does
// nothing.
func (ce *Client) EnableAsync(conf AsyncClientConfig) {
	if ce.async != nil {
		return
	}
	ce.startAsync(conf)
}

// startAsync turns fetching on.
func (ce *Client) startAsync(conf AsyncClientConfig) {
	if conf.FetchInterval <= 0 {
		conf.FetchInterval = AsyncDefaultFetchInterval
	}
	conf.Replicas = asyncReplicas(conf.Replicas)
	ce.async = &asyncClient{conf: conf, kick: make(chan struct{}, 1), handlers: map[uint16]AsyncHandler{}}
	// A new session may be to a server holding messages for us.
	prev := ce.EntityCommon.setSessionCallback
	ce.EntityCommon.setSessionCallback = func(ctx context.Context) error {
		var err error
		if prev != nil {
			err = prev(ctx)
		}
		ce.async.poke()
		return err
	}
	go ce.asyncLoop()
}

func (a *asyncClient) poke() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *asyncClient) ports() []uint16 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]uint16, 0, len(a.handlers))
	for p := range a.handlers {
		out = append(out, p)
	}
	return out
}

func (a *asyncClient) handler(port uint16) AsyncHandler {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.handlers[port]
}

// HandleAsync sets the handler for messages on port, replacing any before
// it, or removes it when h is nil. It needs fetching on (Config.Async).
func (ce *Client) HandleAsync(port uint16, h AsyncHandler) error {
	if ce.async == nil {
		return ErrAsyncDisabled
	}
	if port == 0 {
		return ErrAsyncBadRequest
	}
	ce.async.mu.Lock()
	if h == nil {
		delete(ce.async.handlers, port)
	} else {
		ce.async.handlers[port] = h
	}
	ce.async.mu.Unlock()
	ce.async.poke()
	return nil
}

// FetchAsyncSoon asks the fetch loop to look now, for example when the
// network came back.
func (ce *Client) FetchAsyncSoon() {
	if ce.async != nil {
		ce.async.poke()
	}
}

func (ce *Client) asyncLoop() {
	select {
	case <-ce.ready:
	case <-ce.done:
		return
	}
	t := time.NewTicker(ce.async.conf.FetchInterval)
	defer t.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), ce.async.conf.FetchInterval)
		go func() {
			select {
			case <-ce.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		if n, err := ce.FetchAsync(ctx); err != nil && !errors.Is(err, ErrAsyncNoServers) {
			ce.log.WithError(err).Debug("dmsg-async: fetch")
		} else if n > 0 {
			ce.log.WithField("messages", n).Info("dmsg-async: fetched messages left while offline")
		}
		cancel()
		select {
		case <-ce.done:
			return
		case <-t.C:
		case <-ce.async.kick:
		}
	}
}

// asyncServers lists the servers in discovery that hold messages.
func (ce *Client) asyncServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := ce.dc.AllServers(ctx)
	if err != nil {
		return nil, err
	}
	out := entries[:0:0]
	for _, e := range entries {
		if e != nil && e.Server != nil && e.Server.Async != nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// Deposit leaves payload for dst on port with the servers that hold dst's
// messages, and returns the ones that took it. It fails only when none did.
// The payload is opaque to the servers: seal it first if it is private.
func (ce *Client) Deposit(ctx context.Context, dst cipher.PubKey, port uint16, payload []byte) ([]cipher.PubKey, error) {
	if dst.Null() || port == 0 {
		return nil, ErrAsyncBadRequest
	}
	entries, err := ce.asyncServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("dmsg-async: list servers: %w", err)
	}
	replicas := AsyncDefaultReplicas
	if ce.async != nil {
		replicas = ce.async.conf.Replicas
	}
	targets := AsyncServersFor(entries, dst, replicas)
	if len(targets) == 0 {
		return nil, ErrAsyncNoServers
	}
	var (
		stored []cipher.PubKey
		errs   []error
	)
	for _, e := range targets {
		if len(payload) > e.Server.Async.MaxMessage {
			errs = append(errs, fmt.Errorf("%s: %w", e.Static, ErrAsyncTooBig))
			continue
		}
		if err := ce.depositOn(ctx, e.Static, dst, port, payload); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Static, err))
			continue
		}
		stored = append(stored, e.Static)
	}
	if len(stored) == 0 {
		return nil, errors.Join(errs...)
	}
	return stored, nil
}

func (ce *Client) depositOn(ctx context.Context, srv, dst cipher.PubKey, port uint16, payload []byte) error {
	_, hadBefore := ce.clientSession(ce.porter, srv)
	ses, err := ce.EnsureAndObtainSession(ctx, srv)
	if err != nil {
		return err
	}
	defer ce.closeIfDialed(srv, ses, hadBefore)
	_, err = ses.asyncDeposit(dst, port, payload)
	return err
}

// FetchAsync takes what the servers hold for this client now, hands each
// message to its port's handler and acks the ones handled. It reads the
// servers placement picks for this client and any other async server it
// already has a session to, and reports how many messages were handled.
func (ce *Client) FetchAsync(ctx context.Context) (int, error) {
	if ce.async == nil {
		return 0, ErrAsyncDisabled
	}
	ports := ce.async.ports()
	if len(ports) == 0 {
		return 0, nil
	}
	ce.async.fetchMu.Lock()
	defer ce.async.fetchMu.Unlock()

	entries, err := ce.asyncServers(ctx)
	if err != nil {
		return 0, fmt.Errorf("dmsg-async: list servers: %w", err)
	}
	var servers []cipher.PubKey
	for _, e := range AsyncServersFor(entries, ce.pk, ce.async.conf.Replicas) {
		servers = append(servers, e.Static)
	}
	for _, e := range entries {
		if _, ok := ce.clientSession(ce.porter, e.Static); ok && !slices.Contains(servers, e.Static) {
			servers = append(servers, e.Static)
		}
	}
	if len(servers) == 0 {
		return 0, ErrAsyncNoServers
	}
	total := 0
	var errs []error
	for _, srv := range servers {
		n, err := ce.fetchFrom(ctx, srv, ports)
		total += n
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", srv, err))
		}
	}
	return total, errors.Join(errs...)
}

func (ce *Client) fetchFrom(ctx context.Context, srv cipher.PubKey, ports []uint16) (int, error) {
	_, hadBefore := ce.clientSession(ce.porter, srv)
	ses, err := ce.EnsureAndObtainSession(ctx, srv)
	if err != nil {
		return 0, err
	}
	defer ce.closeIfDialed(srv, ses, hadBefore)
	total := 0
	for {
		n, more, err := ses.asyncFetch(ports, func(m AsyncMessage) bool {
			h := ce.async.handler(m.Port)
			if h == nil {
				return false
			}
			m.Server = srv
			if err := h(m); err != nil {
				ce.log.WithError(err).WithField("port", m.Port).WithField("from", m.From).
					Debug("dmsg-async: handler kept the message for the next fetch")
				return false
			}
			return true
		})
		total += n
		// Stop when a batch handled nothing, or the same messages would
		// come back in the next one.
		if err != nil || !more || n == 0 || ctx.Err() != nil {
			return total, err
		}
	}
}

// openAsync opens a dmsg-async stream with the session's server.
func (cs *ClientSession) openAsync() (*Stream, error) {
	dStr, err := newInitiatingStream(cs)
	if err != nil {
		return nil, err
	}
	lPort, closeFn, err := cs.porter.ReserveEphemeral(context.Background(), dStr)
	if err != nil {
		_ = dStr.Close() //nolint:errcheck
		return nil, err
	}
	dStr.closeMu.Lock()
	dStr.close = closeFn
	dStr.closeMu.Unlock()
	if err := dStr.prepareFields(true, Addr{PK: cs.LocalPK(), Port: lPort}, Addr{PK: cs.RemotePK(), Port: 1}); err != nil {
		_ = dStr.Close() //nolint:errcheck
		return nil, err
	}
	req := StreamRequest{Timestamp: time.Now().UnixNano(), SrcAddr: dStr.lAddr, DstAddr: dStr.rAddr, Async: true}
	obj, err := MakeSignedStreamRequest(&req, cs.localSK())
	if err == nil {
		err = cs.writeObject(dStr.muxStream(), obj)
	}
	if err != nil {
		_ = dStr.Close() //nolint:errcheck
		return nil, err
	}
	return dStr, nil
}

// asyncDeposit leaves payload for dst on port with this session's server.
func (cs *ClientSession) asyncDeposit(dst cipher.PubKey, port uint16, payload []byte) (string, error) {
	dStr, err := cs.openAsync()
	if err != nil {
		return "", err
	}
	defer dStr.Close() //nolint:errcheck
	str := dStr.muxStream()
	_ = str.SetDeadline(time.Now().Add(asyncFrameTimeout * asyncPayloadReadTimeoutFactor)) //nolint:errcheck
	if err := writeAsyncJSON(str, asyncOp{Op: asyncOpDeposit, To: dst, Port: port, Size: len(payload)}); err != nil {
		return "", err
	}
	if err := writeAsyncFrame(str, payload); err != nil {
		return "", err
	}
	var r asyncReply
	if err := readAsyncJSON(str, &r); err != nil {
		return "", asyncUnsupported(err)
	}
	return r.ID, r.err()
}

// asyncFetch takes one batch from this session's server for ports, hands
// each message to handle and acks the ones it took.
func (cs *ClientSession) asyncFetch(ports []uint16, handle func(AsyncMessage) bool) (handled int, more bool, err error) {
	dStr, err := cs.openAsync()
	if err != nil {
		return 0, false, err
	}
	defer dStr.Close() //nolint:errcheck
	str := dStr.muxStream()
	_ = str.SetDeadline(time.Now().Add(asyncFrameTimeout)) //nolint:errcheck
	if err := writeAsyncJSON(str, asyncOp{Op: asyncOpFetch, Ports: ports}); err != nil {
		return 0, false, err
	}
	var r asyncReply
	if err := readAsyncJSON(str, &r); err != nil {
		return 0, false, asyncUnsupported(err)
	}
	if err := r.err(); err != nil {
		return 0, false, err
	}
	if len(r.Messages) == 0 {
		return 0, false, nil
	}
	msgs := make([]AsyncMessage, 0, len(r.Messages))
	for _, h := range r.Messages {
		_ = str.SetDeadline(time.Now().Add(asyncFrameTimeout * asyncPayloadReadTimeoutFactor)) //nolint:errcheck
		payload, err := readAsyncFrame(str, max(h.Size, 0))
		if err != nil {
			return 0, false, err
		}
		if len(payload) != h.Size {
			return 0, false, ErrAsyncBadRequest
		}
		msgs = append(msgs, AsyncMessage{AsyncHeader: h, Payload: payload, Server: cs.RemotePK()})
	}
	var ids []string
	for _, m := range msgs {
		if handle(m) {
			ids = append(ids, m.ID)
		}
	}
	_ = str.SetDeadline(time.Now().Add(asyncFrameTimeout)) //nolint:errcheck
	if err := writeAsyncJSON(str, asyncOp{Op: asyncOpAck, IDs: ids}); err != nil {
		return 0, false, err
	}
	var done asyncReply
	if err := readAsyncJSON(str, &done); err != nil {
		return 0, false, err
	}
	return len(ids), r.More, done.err()
}

// asyncUnsupported turns a server closing the stream on an async request,
// as one without dmsg-async does, into ErrAsyncDisabled.
func asyncUnsupported(err error) error {
	if errors.Is(err, ErrAsyncBadRequest) {
		return err
	}
	return fmt.Errorf("%w (%v)", ErrAsyncDisabled, err)
}
