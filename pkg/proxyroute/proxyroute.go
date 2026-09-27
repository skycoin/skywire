// Package proxyroute pkg/proxyroute/proxyroute.go
//
// Per-domain upstream selection for the resolving proxies. A resolver forwards
// every host it does not resolve itself to one upstream SOCKS5 proxy (normally
// skysocks-client). Rules pick a different upstream for chosen domains, so one
// browser proxy entry can send example.com out through one exit, a second
// skysocks-client instance for another domain, and leave a third domain direct.
package proxyroute

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/bottle/vnet"
	"golang.org/x/net/proxy"
)

// Direct is the upstream value that dials the destination from this host
// instead of through a SOCKS5 proxy.
const Direct = "direct"

// Rule sends hosts matching Suffix to Upstream.
type Rule struct {
	// Suffix is a domain: "example.com" matches example.com and every
	// subdomain of it. A leading dot is ignored.
	Suffix string `json:"suffix"`
	// Upstream is a SOCKS5 host:port, or "direct".
	Upstream string `json:"upstream"`
}

// String renders the rule the way ParseRule reads it.
func (r Rule) String() string { return r.Suffix + "=" + r.Upstream }

// ParseRule reads "suffix=upstream".
func ParseRule(s string) (Rule, error) {
	suffix, upstream, ok := strings.Cut(s, "=")
	if !ok {
		return Rule{}, fmt.Errorf("rule %q: want suffix=upstream", s)
	}
	r := Rule{Suffix: suffix, Upstream: upstream}
	return r.normalized()
}

func (r Rule) normalized() (Rule, error) {
	r.Suffix = strings.ToLower(strings.Trim(strings.TrimSpace(r.Suffix), "."))
	r.Upstream = strings.TrimSpace(r.Upstream)
	if r.Suffix == "" {
		return Rule{}, fmt.Errorf("rule %q: empty suffix", r.String())
	}
	if r.Upstream == "" {
		return Rule{}, fmt.Errorf("rule %q: empty upstream (use %q to dial directly)", r.String(), Direct)
	}
	if r.Upstream != Direct {
		if _, _, err := net.SplitHostPort(r.Upstream); err != nil {
			return Rule{}, fmt.Errorf("rule %q: upstream must be host:port or %q", r.String(), Direct)
		}
	}
	return r, nil
}

// Normalize validates rules and orders them most specific first, so the first
// match is the longest suffix. A later rule for the same suffix replaces an
// earlier one.
func Normalize(rules []Rule) ([]Rule, error) {
	bySuffix := map[string]Rule{}
	for _, r := range rules {
		n, err := r.normalized()
		if err != nil {
			return nil, err
		}
		bySuffix[n.Suffix] = n
	}
	out := make([]Rule, 0, len(bySuffix))
	for _, r := range bySuffix {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Suffix) != len(out[j].Suffix) {
			return len(out[i].Suffix) > len(out[j].Suffix)
		}
		return out[i].Suffix < out[j].Suffix
	})
	return out, nil
}

// Pick returns the upstream for host: the longest matching rule, else def.
// host may carry a port. An empty result means dial directly.
func Pick(rules []Rule, def, host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, r := range rules {
		s := strings.ToLower(strings.Trim(r.Suffix, "."))
		if host == s || strings.HasSuffix(host, "."+s) {
			if r.Upstream == Direct {
				return ""
			}
			return r.Upstream
		}
	}
	return def
}

// upstreamCooldown is how long forwarded dials fast-fail after a recent
// upstream failure, so a burst of requests during the boot window (when the
// upstream, e.g. skysocks-client on :1080, is not yet connected and refuses
// the connection) doesn't each pay a full refused dial. Short by design —
// the client retries.
const upstreamCooldown = 500 * time.Millisecond

// Forwarder dials a destination through the upstream its rules pick. It
// caches one SOCKS5 dialer per upstream (proxy.SOCKS5 returns a dialer that
// is safe to share), and after a failed dial fast-fails that upstream for
// upstreamCooldown rather than blocking or queueing requests. There is no
// clean "skysocks-client connected" signal to wait on, so the cached dialer
// plus cooldown stands in for one.
type Forwarder struct {
	def   string
	rules []Rule

	mu       sync.Mutex
	dialers  map[string]proxy.Dialer
	failedAt map[string]time.Time
}

// NewForwarder builds a Forwarder. def is the upstream for hosts no rule
// matches; empty means direct. rules must already be Normalized.
func NewForwarder(def string, rules []Rule) *Forwarder {
	return &Forwarder{
		def:      def,
		rules:    rules,
		dialers:  map[string]proxy.Dialer{},
		failedAt: map[string]time.Time{},
	}
}

// Active reports whether the forwarder sends anything anywhere but direct.
func (f *Forwarder) Active() bool { return f != nil && (f.def != "" || len(f.rules) > 0) }

// Upstream returns the upstream Dial would use for host, empty for direct.
func (f *Forwarder) Upstream(host string) string { return Pick(f.rules, f.def, host) }

// Dial connects to addr (host:port) through the upstream its host picks.
func (f *Forwarder) Dial(network, addr string) (net.Conn, error) {
	up := f.Upstream(addr)
	if up == "" {
		return vnet.DialTimeout(network, addr, dialTimeout)
	}
	f.mu.Lock()
	if t := f.failedAt[up]; !t.IsZero() && time.Since(t) < upstreamCooldown {
		f.mu.Unlock()
		return nil, fmt.Errorf("upstream SOCKS %s not ready (cooling down)", up)
	}
	d := f.dialers[up]
	if d == nil {
		nd, err := proxy.SOCKS5("tcp", up, nil, vnetDialer{})
		if err != nil {
			f.mu.Unlock()
			return nil, err
		}
		f.dialers[up] = nd
		d = nd
	}
	f.mu.Unlock()

	conn, err := d.Dial(network, addr)
	f.mu.Lock()
	if err != nil {
		f.failedAt[up] = time.Now()
	} else {
		delete(f.failedAt, up)
	}
	f.mu.Unlock()
	return conn, err
}

// dialTimeout bounds one dial to an upstream or a direct destination.
const dialTimeout = 20 * time.Second

// vnetDialer reaches an upstream through bottle/vnet: a real socket natively,
// and under js/wasm the page's virtual loopback, where a browser visor's
// skysocks-client listens. net.Dial cannot reach that loopback, so a resolver
// in a browser tab could never forward to its own skysocks-client.
type vnetDialer struct{}

func (vnetDialer) Dial(network, addr string) (net.Conn, error) {
	return vnet.DialTimeout(network, addr, dialTimeout)
}
