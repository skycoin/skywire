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
		v.svcHosts.set(service, cipher.PubKey{})
		return cipher.PubKey{}, fmt.Errorf("find the host of %s: %w", service, err)
	}
	var host cipher.PubKey
	for _, e := range entries {
		if e != nil && tptypes.IsLocal(e.Type) {
			host = e.RemoteEdge(service)
			break
		}
	}
	v.svcHosts.set(service, host)
	if host.Null() {
		return cipher.PubKey{}, errNoServiceHost
	}
	return host, nil
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

func (s *serviceHosts) set(service, host cipher.PubKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hosts == nil {
		s.hosts = make(map[cipher.PubKey]serviceHost)
	}
	h := s.hosts[service]
	h.host, h.checked = host, time.Now()
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
