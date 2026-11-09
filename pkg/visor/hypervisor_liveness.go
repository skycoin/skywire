// Package visor pkg/visor/hypervisor_liveness.go c3-vis-core
package visor

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// A managed visor is in one of the alerts.State values. visorState is the
// one place that decides it, for hv ls, the node list and the alerts.
// When a link drops without a goodbye, the hypervisor calls the visor back
// over skynet and then dmsg before it calls it offline.

const (
	// dialBackTimeout bounds opening one call back conn.
	dialBackTimeout = 5 * time.Second
	// dialBackCallTimeout bounds the Overview call on it.
	dialBackCallTimeout = 6 * time.Second
	// dialBackParallel bounds concurrent call backs.
	dialBackParallel = 8
	// dialBackSlowAfter is how long a visor may be down before it is
	// called back less often, every dialBackSlowEvery.
	dialBackSlowAfter = 10 * time.Minute
	dialBackSlowEvery = 10 * time.Minute
	// dialBackMisses is how many call backs in a row must go unanswered
	// before a visor that answered before counts as offline.
	dialBackMisses = 2
)

// dialBackResult is the outcome of one call back.
type dialBackResult int

const (
	dialNoAnswer dialBackResult = iota
	// dialListsUs is a visor that answered and still lists this hypervisor.
	dialListsUs
	// dialDetached is a visor that answered without listing this
	// hypervisor, or refused it.
	dialDetached
	// dialUnverified is a visor that is up but serves no RPC to ask.
	dialUnverified
)

// visorLink is what the hypervisor knows about a link beyond the summary
// poll: the visor's goodbye and the last call back.
type visorLink struct {
	bye      string
	byeAt    time.Time
	check    string
	misses   int
	checkAt  time.Time
	checking bool
}

type linkTable struct {
	mu  sync.Mutex
	m   map[cipher.PubKey]*visorLink
	sem chan struct{}
}

func (t *linkTable) get(pk cipher.PubKey) *visorLink {
	if t.m == nil {
		t.m = map[cipher.PubKey]*visorLink{}
	}
	l := t.m[pk]
	if l == nil {
		l = &visorLink{}
		t.m[pk] = l
	}
	return l
}

// linkUp forgets what is known about pk's last link, it is connected again.
func (hv *Hypervisor) linkUp(pk cipher.PubKey) {
	hv.links.mu.Lock()
	delete(hv.links.m, pk)
	hv.links.mu.Unlock()
}

// offlineDelay is the alerts' offline delay, which is also how long a
// restarting visor is given.
func (hv *Hypervisor) offlineDelay() time.Duration {
	if hv.visor != nil {
		if svc, err := hv.visor.alertService(); err == nil {
			s := svc.Settings()
			return s.Delay()
		}
	}
	return alerts.DefaultOfflineAfter
}

// linkFresh reports a visor connected now whose summary poll answers, or
// one that just connected and has not been polled yet.
func (hv *Hypervisor) linkFresh(pk cipher.PubKey, now time.Time) bool {
	hv.mu.RLock()
	_, connected := hv.remoteVisors[pk]
	hv.mu.RUnlock()
	if !connected {
		return false
	}
	hv.summaryCacheMx.RLock()
	c, ok := hv.summaryCache[pk]
	hv.summaryCacheMx.RUnlock()
	return !ok || now.Sub(c.seenAt) < alertFreshWindow
}

// visorState is the state of a managed visor at now.
func (hv *Hypervisor) visorState(pk cipher.PubKey, now time.Time) string {
	if hv.linkFresh(pk, now) {
		return alerts.StateOnline
	}
	hv.links.mu.Lock()
	defer hv.links.mu.Unlock()
	l := hv.links.m[pk]
	switch {
	case l == nil:
		return alerts.StateOffline
	case l.bye == alerts.StateDetached:
		return alerts.StateDetached
	case l.bye == alerts.StateRestarting && now.Sub(l.byeAt) < hv.offlineDelay():
		return alerts.StateRestarting
	case l.check != "":
		return l.check
	}
	return alerts.StateOffline
}

// managedPKs lists every visor this hypervisor manages or remembers.
func (hv *Hypervisor) managedPKs() []cipher.PubKey {
	set := map[cipher.PubKey]bool{}
	hv.mu.RLock()
	for pk := range hv.remoteVisors {
		set[pk] = true
	}
	hv.mu.RUnlock()
	hv.summaryCacheMx.RLock()
	for pk := range hv.summaryCache {
		set[pk] = true
	}
	hv.summaryCacheMx.RUnlock()
	if hv.visor != nil && hv.visor.conf != nil {
		delete(set, hv.visor.conf.PK)
	}
	out := make([]cipher.PubKey, 0, len(set))
	for pk := range set {
		out = append(out, pk)
	}
	return out
}

// checkLinks calls back every visor whose link is down and not explained
// by a goodbye. It returns without waiting for the call backs.
func (hv *Hypervisor) checkLinks(ctx context.Context, now time.Time) {
	delay := hv.offlineDelay()
	for _, pk := range hv.managedPKs() {
		if hv.linkFresh(pk, now) {
			hv.links.mu.Lock()
			if l := hv.links.m[pk]; l != nil {
				l.check, l.misses = "", 0
			}
			hv.links.mu.Unlock()
			continue
		}
		hv.summaryCacheMx.RLock()
		seen := hv.summaryCache[pk].seenAt
		hv.summaryCacheMx.RUnlock()

		hv.links.mu.Lock()
		if hv.links.sem == nil {
			hv.links.sem = make(chan struct{}, dialBackParallel)
		}
		l := hv.links.get(pk)
		skip := l.checking || l.bye == alerts.StateDetached ||
			(l.bye == alerts.StateRestarting && now.Sub(l.byeAt) < delay) ||
			(now.Sub(seen) > dialBackSlowAfter && now.Sub(l.checkAt) < dialBackSlowEvery)
		if !skip {
			l.checking = true
		}
		sem := hv.links.sem
		hv.links.mu.Unlock()
		if skip {
			continue
		}
		go func(pk cipher.PubKey) {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				hv.recordDialBack(pk, dialNoAnswer, false)
				return
			}
			res := hv.dialBackFn()(ctx, pk)
			<-sem
			hv.recordDialBack(pk, res, true)
		}(pk)
	}
}

func (hv *Hypervisor) dialBackFn() func(context.Context, cipher.PubKey) dialBackResult {
	if hv.dialBack != nil {
		return hv.dialBack
	}
	return hv.callBack
}

// recordDialBack stores a call back's outcome. A visor that answered
// before takes dialBackMisses unanswered calls to count as offline.
func (hv *Hypervisor) recordDialBack(pk cipher.PubKey, res dialBackResult, done bool) {
	hv.links.mu.Lock()
	defer hv.links.mu.Unlock()
	l := hv.links.get(pk)
	l.checking = false
	if !done {
		return
	}
	l.checkAt = time.Now()
	switch res {
	case dialListsUs, dialUnverified:
		l.check, l.misses = alerts.StateReconnecting, 0
	case dialDetached:
		l.check, l.misses = alerts.StateDetached, 0
	default:
		l.misses++
		if l.check == "" || l.misses >= dialBackMisses {
			l.check = alerts.StateOffline
		}
	}
}

// recordGoodbye stores a visor's goodbye and drops its link.
func (hv *Hypervisor) recordGoodbye(pk cipher.PubKey, reason string, api visorapi.API) {
	hv.links.mu.Lock()
	l := hv.links.get(pk)
	l.bye, l.byeAt, l.check, l.misses = reason, time.Now(), "", 0
	hv.links.mu.Unlock()

	hv.mu.Lock()
	if c, ok := hv.remoteVisors[pk]; ok && c.API == api {
		delete(hv.remoteVisors, pk)
	}
	if hv.visor != nil {
		if c, ok := hv.visor.remoteVisors[pk]; ok && c.API == api {
			delete(hv.visor.remoteVisors, pk)
		}
	}
	hv.mu.Unlock()
	hv.logger.WithField("visor", pk.String()).WithField("reason", reason).Info("Visor said goodbye")
	_ = api.Close() //nolint:errcheck
}

// watchGoodbye long polls a newly connected visor for its goodbye. It
// returns when the link ends or the visor is too old to answer.
func (hv *Hypervisor) watchGoodbye(pk cipher.PubKey, api visorapi.API) {
	if api == nil {
		return
	}
	for {
		start := time.Now()
		reason, err := api.HypervisorNotice()
		if err != nil {
			return
		}
		switch reason {
		case alerts.StateDetached, alerts.StateRestarting:
			hv.recordGoodbye(pk, reason, api)
			return
		case "":
			// A visor that answers at once is not waiting, do not spin.
			if time.Since(start) < time.Second {
				time.Sleep(hvNoticeWait)
			}
		default:
			return
		}
	}
}

// callBack asks pk whether it still has this hypervisor, over skynet when
// a direct transport exists and then over dmsg, the paths that
// `--via skynet://` and `--via dmsg://` use.
func (hv *Hypervisor) callBack(ctx context.Context, pk cipher.PubKey) dialBackResult {
	v := hv.visor
	if v == nil || v.conf == nil {
		return dialNoAnswer
	}
	var dials []func(context.Context) (io.ReadWriteCloser, error)
	if v.transportRPCMux != nil && hasFastTransportTo(v.tpM, pk) {
		dials = append(dials, func(context.Context) (io.ReadWriteCloser, error) {
			return v.transportRPCMux.Dial(pk, "")
		})
	}
	if v.dmsgC != nil {
		dials = append(dials, func(ctx context.Context) (io.ReadWriteCloser, error) {
			return v.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: skyenv.DmsgVisorRPCPort})
		})
	}
	res := dialNoAnswer
	for _, dial := range dials {
		conn, err := dialWithin(ctx, dialBackTimeout, dial)
		if err != nil {
			if errors.Is(err, dmsg.ErrReqNoListener) || strings.Contains(err.Error(), "no associated listener") {
				res = dialUnverified
			}
			continue
		}
		if r := askHypervisors(conn, v.conf.PK); r != dialNoAnswer {
			return r
		}
	}
	return res
}

// dialWithin runs dial with a timeout, closing a conn that arrives late.
func dialWithin(ctx context.Context, d time.Duration, dial func(context.Context) (io.ReadWriteCloser, error)) (io.ReadWriteCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type result struct {
		c   io.ReadWriteCloser
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := dial(ctx)
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				_ = r.c.Close() //nolint:errcheck
			}
		}()
		return nil, ctx.Err()
	}
}

// askHypervisors asks the visor on conn for its hypervisors and closes
// conn. The conn opened, so the visor is up: one that drops it without
// an answer refused us, one that stays silent did not answer.
func askHypervisors(conn io.ReadWriteCloser, self cipher.PubKey) dialBackResult {
	api := visorapi.NewRPCClient(nil, conn, visorapi.RPCPrefix, dialBackCallTimeout)
	defer api.Close() //nolint:errcheck
	ov, err := api.Overview()
	switch {
	case err == nil && ov != nil:
		if slices.Contains(ov.Hypervisors, self) {
			return dialListsUs
		}
		return dialDetached
	case isServerError(err):
		return dialUnverified
	case err != nil && !errors.Is(err, context.DeadlineExceeded):
		return dialDetached
	}
	return dialNoAnswer
}

func isServerError(err error) bool {
	_, ok := errors.AsType[rpc.ServerError](err)
	return ok
}
