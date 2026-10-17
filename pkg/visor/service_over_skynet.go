// Package visor pkg/visor/service_over_skynet.go
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// serviceHostTTL is how long a service's host is remembered before TPD is
	// asked again, and noServiceHostTTL how long the lack of one is. Hosts
	// rarely change, and every lookup is a TPD request.
	serviceHostTTL   = 30 * time.Minute
	noServiceHostTTL = 10 * time.Minute
	// serviceSkynetBackoff skips skynet for a service after a failed dial, so
	// each request does not wait out the relay search before falling back.
	serviceSkynetBackoff = 2 * time.Minute
	// serviceSkynetTimeout bounds one skynet dial to a service's host.
	serviceSkynetTimeout = 6 * time.Second
)

var errNoServiceHost = errors.New("service has no host visor in the transport graph")

// serviceHosts remembers, per deployment service key, the visor hosting it
// and when skynet may next be tried for it.
type serviceHosts struct {
	mu    sync.Mutex
	hosts map[cipher.PubKey]serviceHost
}

type serviceHost struct {
	host      cipher.PubKey // null when TPD shows none
	sysFeed   bool          // the host labels the link LabelSysFeed
	checked   time.Time
	failUntil time.Time
}

// lookupSkipsSkynet marks a context whose dials go straight to dmsg: the
// TPD query that finds a host must not itself look for TPD's host.
type lookupSkipsSkynet struct{}

// dialServiceOverSkynet reaches a deployment service through the visor that
// hosts it, over a transport to that visor or a 1-hop relay, never a route.
// The host proves it runs the service (skynetweb.PerformServiceHandshake).
func (v *Visor) dialServiceOverSkynet(ctx context.Context, service cipher.PubKey, port uint16) (net.Conn, error) {
	if ctx.Value(lookupSkipsSkynet{}) != nil || v.router == nil || v.tpM == nil || v.skynetFwdMux == nil {
		return nil, errNoRelay
	}
	host, err := v.serviceHost(ctx, service)
	if err != nil {
		return nil, err
	}
	if host == v.conf.PK {
		return nil, errors.New("this visor hosts the service; dmsg reaches it in process")
	}
	dialer := &routerSkynetDialer{
		router:       v.router,
		localPK:      v.conf.PK,
		log:          v.log,
		tpM:          v.tpM,
		skynetMuxPtr: &v.skynetFwdMux,
		service:      service,
	}
	dctx, cancel := context.WithTimeout(ctx, serviceSkynetTimeout)
	defer cancel()
	conn, err := dialer.dialDirectOrRelay(dctx, host, port)
	v.dmsgSkynet.note(service, err)
	if err != nil {
		v.svcHosts.failed(service)
		return nil, fmt.Errorf("service %s via host %s: %w", service, host, err)
	}
	v.log.WithField("service", service.String()).WithField("host", host.String()).WithField("port", port).
		Debug("Reached a deployment service over skynet through its host")
	return conn, nil
}

// serviceHost returns the visor TPD shows a local link to service from,
// cached for serviceHostTTL, or an error while skynet is backed off.
func (v *Visor) serviceHost(ctx context.Context, service cipher.PubKey) (cipher.PubKey, error) {
	if h, ok := v.svcHosts.get(service); ok {
		if time.Now().Before(h.failUntil) {
			return cipher.PubKey{}, errors.New("skynet to this service failed recently")
		}
		if h.host.Null() {
			return cipher.PubKey{}, errNoServiceHost
		}
		return h.host, nil
	}
	dc := v.tpM.Conf.DiscoveryClient
	if dc == nil {
		return cipher.PubKey{}, errNoServiceHost
	}
	lctx, cancel := context.WithTimeout(context.WithValue(ctx, lookupSkipsSkynet{}, true), relayDiscoveryTimeout)
	defer cancel()
	entries, err := dc.GetTransportsByEdge(lctx, service)
	if err != nil {
		v.svcHosts.set(service, cipher.PubKey{}, false)
		return cipher.PubKey{}, fmt.Errorf("find the host of %s: %w", service, err)
	}
	var (
		host    cipher.PubKey
		sysFeed bool
	)
	for _, e := range entries {
		if e != nil && tptypes.IsLocal(e.Type) {
			host, sysFeed = e.RemoteEdge(service), e.Label == transport.LabelSysFeed
			break
		}
	}
	v.svcHosts.set(service, host, sysFeed)
	if host.Null() {
		return cipher.PubKey{}, errNoServiceHost
	}
	return host, nil
}

// serviceFeedHost returns service's host and whether the host labels the link
// to it LabelSysFeed, meaning the service reads the system feed of the
// transport discovery in the host's process.
func (v *Visor) serviceFeedHost(ctx context.Context, service cipher.PubKey) (cipher.PubKey, bool, error) {
	host, err := v.serviceHost(ctx, service)
	if err != nil {
		return host, false, err
	}
	h, _ := v.svcHosts.get(service)
	return host, h.sysFeed, nil
}

func (s *serviceHosts) get(service cipher.PubKey) (serviceHost, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hosts[service]
	ttl := serviceHostTTL
	if h.host.Null() {
		ttl = noServiceHostTTL
	}
	if !ok || time.Since(h.checked) > ttl {
		return serviceHost{}, false
	}
	return h, true
}

func (s *serviceHosts) set(service, host cipher.PubKey, sysFeed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hosts == nil {
		s.hosts = make(map[cipher.PubKey]serviceHost)
	}
	h := s.hosts[service]
	h.host, h.sysFeed, h.checked = host, sysFeed, time.Now()
	s.hosts[service] = h
}

func (s *serviceHosts) failed(service cipher.PubKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.hosts[service]; ok {
		h.failUntil = time.Now().Add(serviceSkynetBackoff)
		s.hosts[service] = h
	}
}

// cxoDial is how the visor's CXO feeds dial their consumers: a deployment
// service through its host over skynet when it can, else dmsg.
func (v *Visor) cxoDial(ctx context.Context, addr dmsg.Addr) (net.Conn, error) {
	if v.isDmsgServiceKey(addr.PK) {
		// Leave dmsg at least half of the caller's time.
		sctx, cancel := ctx, context.CancelFunc(func() {})
		if dl, ok := ctx.Deadline(); ok {
			sctx, cancel = context.WithTimeout(ctx, time.Until(dl)/2)
		}
		conn, err := v.dialServiceOverSkynet(sctx, addr.PK, addr.Port)
		cancel()
		if err == nil {
			return conn, nil
		}
	}
	return v.dmsgC.Dial(ctx, addr)
}

// feedRedialEvery spaces the attempts to move one feed's conn off dmsg.
const feedRedialEvery = 10 * time.Minute

// cxoRedial moves a feed's conn to a deployment service off dmsg once the
// visor has a skynet path to the service's host. Feeds connect at startup,
// often before that path exists, and a live conn is otherwise kept for good.
func (v *Visor) cxoRedial(ctx context.Context, peer cipher.PubKey, addr net.Addr) bool {
	if _, overDmsg := addr.(dmsg.Addr); !overDmsg || !v.isDmsgServiceKey(peer) {
		return false
	}
	host, err := v.serviceHost(ctx, peer)
	if err != nil || host == v.conf.PK {
		return false
	}
	return v.feedRedials.due(addr.String())
}

// feedRedials remembers when each feed conn last tried leaving dmsg.
type feedRedials struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (f *feedRedials) due(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.last[key]; ok && time.Since(t) < feedRedialEvery {
		return false
	}
	if f.last == nil {
		f.last = make(map[string]time.Time)
	}
	f.last[key] = time.Now()
	return true
}
