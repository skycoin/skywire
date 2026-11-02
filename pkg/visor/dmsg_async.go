// Package visor pkg/visor/dmsg_async.go c3-vis-core
//
// dmsg-async on the visor (docs/design/dmsg-async.md). As an inbox host the
// visor holds messages for keys it accepts: its own, the visors it manages as
// a hypervisor, and the configured extras. As a recipient it registers with
// its inbox hosts (its hypervisors unless configured), publishes its record
// to dmsg discovery, and fetches what waits for the ports it has handlers
// for. As a sender it deposits for a key that cannot be reached.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	asyncCycle          = 10 * time.Minute
	asyncReregister     = 24 * time.Hour
	asyncRetryFailed    = time.Hour
	asyncRecordRenew    = 7 * 24 * time.Hour
	asyncOpTimeout      = 90 * time.Second
	asyncRecordCacheTTL = time.Hour
)

// ErrDmsgAsyncOff means dmsg-async is not running on this visor.
var ErrDmsgAsyncOff = errors.New("dmsg-async is not running")

type asyncState struct {
	mu sync.Mutex
	// cycle serializes inbox cycles, so two never sign and register at once.
	cycle    sync.Mutex
	log      *logging.Logger
	host     *dmsgasync.Host
	store    string
	inbox    bool
	hosts    []cipher.PubKey
	publish  bool
	policy   dmsgasync.Policy
	record   *dmsgasync.Record
	reg      map[cipher.PubKey]time.Time
	regErr   map[cipher.PubKey]string
	pubAt    time.Time
	pubErr   string
	round    time.Time
	lastFet  time.Time
	fetched  int
	handlers map[uint16]dmsgasync.Handler
	cache    map[cipher.PubKey]cachedRecord
	kick     chan struct{}
}

type cachedRecord struct {
	rec dmsgasync.Record
	at  time.Time
}

// asyncDir is "dmsg-async" beside the local path, so a wasm visor keeps it.
func asyncDir(conf *visorconfig.V1) string {
	if conf.DmsgAsync != nil && conf.DmsgAsync.Host.Dir != "" {
		return conf.DmsgAsync.Host.Dir
	}
	return filepath.Join(filepath.Dir(filepath.Clean(conf.LocalPath)), "dmsg-async")
}

func (v *Visor) asyncDial(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	// dmsg does not loop back, so a deposit on this visor's own host is
	// served in process, as from this visor's own key.
	if pk == v.conf.PK && port == dmsgasync.Port {
		v.async.mu.Lock()
		h := v.async.host
		v.async.mu.Unlock()
		if h == nil {
			return nil, ErrDmsgAsyncOff
		}
		c, s := net.Pipe()
		go h.Handle(s, v.conf.PK)
		return c, nil
	}
	if v.dmsgC == nil {
		return nil, ErrDmsgAsyncOff
	}
	return v.dmsgC.DialStream(ctx, dmsg.Addr{PK: pk, Port: port})
}

// asyncHosts are the inbox hosts: configured, else this visor's hypervisors.
func asyncHosts(conf *visorconfig.V1) []cipher.PubKey {
	var hosts []cipher.PubKey
	if conf.DmsgAsync != nil && len(conf.DmsgAsync.Inbox.Hosts) > 0 {
		hosts = conf.DmsgAsync.Inbox.Hosts
	} else {
		hosts = conf.GetHypervisors()
	}
	out := make([]cipher.PubKey, 0, len(hosts))
	for _, h := range hosts {
		if h != conf.PK && !h.Null() && !slices.Contains(out, h) && len(out) < dmsgasync.MaxRecordHosts {
			out = append(out, h)
		}
	}
	return out
}

func initDmsgAsync(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		return nil
	}
	conf := v.conf.DmsgAsync
	if conf == nil {
		conf = &visorconfig.DmsgAsyncConfig{}
	}
	a := &v.async
	a.mu.Lock()
	a.handlers = map[uint16]dmsgasync.Handler{}
	a.cache = map[cipher.PubKey]cachedRecord{}
	a.reg = map[cipher.PubKey]time.Time{}
	a.regErr = map[cipher.PubKey]string{}
	a.kick = make(chan struct{}, 1)
	a.log = log
	a.mu.Unlock()

	hostOn := v.conf.Hypervisor != nil
	if conf.Host.Enable != nil {
		hostOn = *conf.Host.Enable
	}
	if hostOn {
		if err := v.startAsyncHost(ctx, conf.Host, log); err != nil {
			log.WithError(err).Warn("dmsg-async: host not started")
		}
	}

	hosts := asyncHosts(v.conf)
	if conf.Inbox.Disable || len(hosts) == 0 {
		return nil
	}
	a.mu.Lock()
	a.inbox = true
	a.hosts = hosts
	a.publish = conf.Inbox.Publish == nil || *conf.Inbox.Publish
	a.policy = dmsgasync.Policy{Allow: conf.Inbox.Allow}
	a.mu.Unlock()
	go v.asyncInboxLoop(ctx, log)
	return nil
}

func (v *Visor) startAsyncHost(ctx context.Context, conf visorconfig.DmsgAsyncHostConfig, log *logging.Logger) error {
	store := conf.Store
	if store == "" {
		store = dmsgasync.StoreDisk
	}
	hosted := append([]cipher.PubKey(nil), conf.Hosted...)
	h, err := dmsgasync.NewHost(dmsgasync.HostConfig{
		PK:    v.conf.PK,
		Store: store,
		Dir:   asyncDir(v.conf),
		Limits: dmsgasync.Limits{
			MaxMessage:        conf.MaxMessage,
			MaxPerRecipient:   conf.MaxPerRecipient,
			MaxRecipientBytes: conf.MaxRecipientBytes,
			MaxTotal:          conf.MaxTotalSize,
			TTL:               time.Duration(conf.TTL),
		},
		Accepts: func(pk cipher.PubKey) bool {
			return pk == v.conf.PK || slices.Contains(hosted, pk) || slices.Contains(v.managedVisorPKs(), pk)
		},
		Log: log,
	})
	if err != nil {
		return err
	}
	l, err := v.dmsgC.Listen(dmsgasync.Port)
	if err != nil {
		return fmt.Errorf("listen on dmsg port %d: %w", dmsgasync.Port, err)
	}
	v.pushCloseStack("dmsg_async.host", l.Close)
	go func() {
		if err := h.Serve(ctx, l); err != nil {
			log.WithError(err).Debug("dmsg-async: host stopped")
		}
	}()
	v.async.mu.Lock()
	v.async.host = h
	v.async.store = store
	v.async.mu.Unlock()
	log.WithField("store", store).Info("dmsg-async: inbox host running")
	return nil
}

func (v *Visor) asyncInboxLoop(ctx context.Context, log *logging.Logger) {
	t := time.NewTicker(asyncCycle)
	defer t.Stop()
	for {
		v.asyncCycle(ctx, log, false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-v.async.kick:
		}
	}
}

// asyncCycle keeps the registrations and the record fresh, publishes and
// fetches. force does it all regardless of age.
func (v *Visor) asyncCycle(ctx context.Context, log *logging.Logger, force bool) {
	a := &v.async
	a.cycle.Lock()
	defer a.cycle.Unlock()
	now := time.Now()
	a.mu.Lock()
	due := force || a.record == nil || a.record.Expires.Sub(now) < asyncRecordRenew ||
		now.Sub(a.round) > asyncReregister || len(a.regErr) > 0 && now.Sub(a.round) > asyncRetryFailed
	a.mu.Unlock()
	if due {
		v.asyncRegister(ctx, log, now)
	}

	a.mu.Lock()
	rec := a.record
	pubDue := a.publish && rec != nil && len(rec.Hosts) > 0 && (force || a.pubAt.IsZero() || now.Sub(a.pubAt) > asyncReregister)
	a.mu.Unlock()
	if pubDue {
		err := v.asyncPublish(ctx, *rec)
		a.mu.Lock()
		if err != nil {
			a.pubErr = err.Error()
		} else {
			a.pubAt = time.Now()
			a.pubErr = ""
		}
		a.mu.Unlock()
	}
	v.asyncFetch(ctx, log)
}

// asyncRegister registers with every candidate host under a record naming
// them all, then re-signs the record with only the hosts that accepted and
// registers that with them, so senders are never sent to a host that refuses.
func (v *Visor) asyncRegister(ctx context.Context, log *logging.Logger, now time.Time) {
	a := &v.async
	a.mu.Lock()
	candidates := append([]cipher.PubKey(nil), a.hosts...)
	pol := a.policy
	a.mu.Unlock()
	seq := uint64(now.UnixNano()) //nolint:gosec

	register := func(rec dmsgasync.Record, hosts []cipher.PubKey) (ok []cipher.PubKey, failed map[cipher.PubKey]string) {
		failed = map[cipher.PubKey]string{}
		for _, h := range hosts {
			cctx, cancel := context.WithTimeout(ctx, asyncOpTimeout)
			err := dmsgasync.Register(cctx, v.asyncDial, h, rec, pol)
			cancel()
			if err != nil {
				failed[h] = err.Error()
				log.WithError(err).WithField("host", h).Debug("dmsg-async: register")
				continue
			}
			ok = append(ok, h)
		}
		return ok, failed
	}

	full, err := dmsgasync.NewRecord(v.conf.PK, v.conf.SK, candidates, 0, seq, 0)
	if err != nil {
		log.WithError(err).Warn("dmsg-async: sign inbox record")
		return
	}
	ok, failed := register(full, candidates)
	rec := full
	if len(ok) != len(candidates) {
		rec, err = dmsgasync.NewRecord(v.conf.PK, v.conf.SK, ok, 0, seq+1, 0)
		if err != nil {
			log.WithError(err).Warn("dmsg-async: sign inbox record")
			return
		}
		if len(ok) > 0 {
			var lost map[cipher.PubKey]string
			ok, lost = register(rec, ok)
			for h, e := range lost {
				failed[h] = e
			}
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	changed := a.record == nil || !slices.Equal(a.record.Hosts, rec.Hosts)
	a.record = &rec
	a.round = now
	a.reg = map[cipher.PubKey]time.Time{}
	for _, h := range ok {
		a.reg[h] = time.Now()
	}
	a.regErr = failed
	if changed {
		a.pubAt = time.Time{}
	}
}

func (v *Visor) asyncDiscURL() (string, bool) {
	pk, ok := dmsgdCXOPeer(v)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("http://%s:%d", pk.Hex(), dmsg.DefaultDmsgHTTPPort), true
}

func (v *Visor) asyncHTTP() *http.Client {
	return &http.Client{Transport: v.dmsgHTTPTransport(), Timeout: asyncOpTimeout}
}

func (v *Visor) asyncPublish(ctx context.Context, rec dmsgasync.Record) error {
	u, ok := v.asyncDiscURL()
	if !ok {
		return errors.New("no dmsg discovery")
	}
	return dmsgasync.PublishRecord(ctx, v.asyncHTTP(), u, rec)
}

// asyncFetch takes what waits on every host for the ports with handlers.
func (v *Visor) asyncFetch(ctx context.Context, log *logging.Logger) {
	a := &v.async
	a.mu.Lock()
	ports := make([]uint16, 0, len(a.handlers))
	for p := range a.handlers {
		ports = append(ports, p)
	}
	hosts := append([]cipher.PubKey(nil), a.hosts...)
	a.mu.Unlock()
	if len(ports) == 0 {
		return
	}
	total := 0
	for _, h := range hosts {
		cctx, cancel := context.WithTimeout(ctx, asyncOpTimeout)
		n, err := dmsgasync.Fetch(cctx, v.asyncDial, h, ports, v.asyncDispatch)
		cancel()
		total += n
		if err != nil {
			log.WithError(err).WithField("host", h).Debug("dmsg-async: fetch")
		}
	}
	a.mu.Lock()
	a.lastFet = time.Now()
	a.fetched += total
	a.mu.Unlock()
}

func (v *Visor) asyncDispatch(m dmsgasync.Message) error {
	v.async.mu.Lock()
	h := v.async.handlers[m.Port]
	v.async.mu.Unlock()
	if h == nil {
		return errors.New("no handler")
	}
	return h(m)
}

// AsyncHandle sets the in-visor handler for messages fetched on port.
func (v *Visor) AsyncHandle(port uint16, h dmsgasync.Handler) {
	v.async.mu.Lock()
	defer v.async.mu.Unlock()
	if v.async.handlers == nil {
		v.async.handlers = map[uint16]dmsgasync.Handler{}
	}
	if h == nil {
		delete(v.async.handlers, port)
		return
	}
	v.async.handlers[port] = h
}

// asyncRecordFor finds to's inbox record: its registration on this visor's own
// host, else a recent one from the cache, else dmsg discovery.
func (v *Visor) asyncRecordFor(ctx context.Context, to cipher.PubKey) (dmsgasync.Record, error) {
	a := &v.async
	a.mu.Lock()
	c, ok := a.cache[to]
	host := a.host
	a.mu.Unlock()
	if host != nil {
		if rec, ok := host.Record(to); ok {
			return rec, nil
		}
	}
	if ok && time.Since(c.at) < asyncRecordCacheTTL && c.rec.Verify(time.Now()) == nil {
		return c.rec, nil
	}
	u, ok := v.asyncDiscURL()
	if !ok {
		return dmsgasync.Record{}, dmsgasync.ErrNoRecord
	}
	rec, err := dmsgasync.FetchRecord(ctx, v.asyncHTTP(), u, to)
	if err != nil {
		return dmsgasync.Record{}, err
	}
	a.mu.Lock()
	if a.cache == nil {
		a.cache = map[cipher.PubKey]cachedRecord{}
	}
	a.cache[to] = cachedRecord{rec: rec, at: time.Now()}
	a.mu.Unlock()
	return rec, nil
}

// DmsgAsyncSend implements visorapi.API.
func (v *Visor) DmsgAsyncSend(req visorapi.DmsgAsyncSendRequest) (*visorapi.DmsgAsyncSendResult, error) {
	if v.dmsgC == nil {
		return nil, ErrDmsgAsyncOff
	}
	if req.To.Null() || req.Port == 0 {
		return nil, dmsgasync.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(context.Background(), asyncOpTimeout)
	defer cancel()
	rec, err := v.asyncRecordFor(ctx, req.To)
	if err != nil {
		return nil, err
	}
	ref := req.Ref
	if ref == "" {
		ref = dmsgasync.NewRef()
	}
	host, err := dmsgasync.DepositAny(ctx, v.asyncDial, rec, req.Port, ref, req.Payload)
	if err != nil {
		return nil, err
	}
	return &visorapi.DmsgAsyncSendResult{Host: host, Ref: ref}, nil
}

// DmsgAsyncTake implements visorapi.API.
func (v *Visor) DmsgAsyncTake(ports []uint16) ([]dmsgasync.Message, error) {
	a := &v.async
	a.mu.Lock()
	inbox := a.inbox
	hosts := append([]cipher.PubKey(nil), a.hosts...)
	for _, p := range ports {
		if a.handlers[p] != nil {
			a.mu.Unlock()
			return nil, fmt.Errorf("port %d has an in-visor handler", p)
		}
	}
	a.mu.Unlock()
	if !inbox {
		return nil, ErrDmsgAsyncOff
	}
	var out []dmsgasync.Message
	var errs []error
	for _, h := range hosts {
		ctx, cancel := context.WithTimeout(context.Background(), asyncOpTimeout)
		_, err := dmsgasync.Fetch(ctx, v.asyncDial, h, ports, func(m dmsgasync.Message) error {
			out = append(out, m)
			return nil
		})
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h, err))
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// DmsgAsyncSync implements visorapi.API.
func (v *Visor) DmsgAsyncSync() error {
	v.async.mu.Lock()
	inbox := v.async.inbox
	v.async.mu.Unlock()
	if !inbox {
		return ErrDmsgAsyncOff
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*asyncOpTimeout)
	defer cancel()
	v.asyncCycle(ctx, v.async.log, true)
	return nil
}

// DmsgAsyncStatus implements visorapi.API.
func (v *Visor) DmsgAsyncStatus() (*visorapi.DmsgAsyncStatus, error) {
	a := &v.async
	a.mu.Lock()
	defer a.mu.Unlock()
	st := &visorapi.DmsgAsyncStatus{}
	if a.host != nil {
		st.Host = &visorapi.DmsgAsyncHostStatus{
			Store:      a.store,
			Limits:     a.host.Limits(),
			Stats:      a.host.Stats(),
			Registered: a.host.Registered(),
		}
	}
	in := visorapi.DmsgAsyncInboxStatus{
		Enabled:    a.inbox,
		Published:  !a.pubAt.IsZero(),
		PublishErr: a.pubErr,
		LastFetch:  a.lastFet,
		Fetched:    a.fetched,
	}
	if a.record != nil {
		rec := *a.record
		in.Record = &rec
	}
	for _, h := range a.hosts {
		in.Hosts = append(in.Hosts, visorapi.DmsgAsyncInboxHost{PK: h, Registered: a.reg[h], Error: a.regErr[h]})
	}
	for p := range a.handlers {
		in.Handlers = append(in.Handlers, p)
	}
	slices.Sort(in.Handlers)
	st.Inbox = in
	return st, nil
}
