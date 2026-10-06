// Package skydns pkg/skydns/upstream.go
package skydns

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	// plainBackoff is how long a resolver with no TLS is asked in plain DNS
	// before TLS is tried again.
	plainBackoff = 5 * time.Minute
	idleConns    = 4
)

// DialFunc opens a connection, inside a tunnel or past it.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Upstream asks one resolver over DNS-over-TLS (RFC 7858). Unless strict, a
// resolver with no TLS on 853 is asked in plain DNS, as Android's automatic
// Private DNS does.
type Upstream struct {
	server string
	dial   DialFunc
	strict bool
	idle   chan *dns.Conn
	roots  *x509.CertPool // nil means the system's

	mu         sync.Mutex
	plainUntil time.Time
}

// NewUpstream returns an Upstream for the resolver at server, an IP address.
func NewUpstream(server string, dial DialFunc, strict bool) *Upstream {
	return &Upstream{server: server, dial: dial, strict: strict, idle: make(chan *dns.Conn, idleConns)}
}

// HasTLS reports whether server is a public resolver known to answer
// DNS-over-TLS, where falling back to plain DNS would only help an attacker.
func HasTLS(server string) bool {
	switch server {
	case "1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "149.112.112.112":
		return true
	}
	return false
}

// errNoTLS is a resolver that could not be reached over TLS at all, as
// opposed to a query that failed on a working connection.
type errNoTLS struct{ err error }

func (e errNoTLS) Error() string { return "skydns: no DNS-over-TLS: " + e.err.Error() }
func (e errNoTLS) Unwrap() error { return e.err }

// Exchange implements [Exchanger].
func (u *Upstream) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	if !u.plainNow() {
		resp, err := u.exchangeTLS(ctx, m)
		var noTLS errNoTLS
		if err == nil || u.strict || !errors.As(err, &noTLS) {
			return resp, err
		}
		u.mu.Lock()
		u.plainUntil = time.Now().Add(plainBackoff)
		u.mu.Unlock()
	}
	resp, err := u.exchangePlain(ctx, "udp", m)
	if err == nil && resp.Truncated {
		return u.exchangePlain(ctx, "tcp", m)
	}
	return resp, err
}

func (u *Upstream) plainNow() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return time.Now().Before(u.plainUntil)
}

func (u *Upstream) exchangeTLS(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	for {
		co, reused, err := u.conn(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := roundTrip(ctx, co, m)
		if err == nil {
			select {
			case u.idle <- co:
			default:
				_ = co.Close() //nolint:errcheck
			}
			return resp, nil
		}
		_ = co.Close() //nolint:errcheck
		// Resolvers drop idle connections, so a pooled one failing earns one
		// more try on a fresh connection.
		if !reused {
			return nil, err
		}
	}
}

// conn returns a pooled connection, or opens one.
func (u *Upstream) conn(ctx context.Context) (co *dns.Conn, reused bool, err error) {
	select {
	case co := <-u.idle:
		return co, true, nil
	default:
	}
	raw, err := u.dial(ctx, "tcp", net.JoinHostPort(u.server, "853"))
	if err != nil {
		return nil, false, errNoTLS{err}
	}
	// An IP as ServerName is checked against the certificate's IP SANs.
	tc := tls.Client(raw, &tls.Config{ServerName: u.server, RootCAs: u.roots, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close() //nolint:errcheck
		return nil, false, errNoTLS{err}
	}
	return &dns.Conn{Conn: tc}, false, nil
}

func (u *Upstream) exchangePlain(ctx context.Context, network string, m *dns.Msg) (*dns.Msg, error) {
	raw, err := u.dial(ctx, network, net.JoinHostPort(u.server, "53"))
	if err != nil {
		return nil, fmt.Errorf("skydns: plain DNS: %w", err)
	}
	defer raw.Close() //nolint:errcheck
	return roundTrip(ctx, &dns.Conn{Conn: raw, UDPSize: dns.DefaultMsgSize}, m)
}

func roundTrip(ctx context.Context, co *dns.Conn, m *dns.Msg) (*dns.Msg, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(queryTimeout)
	}
	if err := co.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if err := co.WriteMsg(m); err != nil {
		return nil, err
	}
	resp, err := co.ReadMsg()
	if err != nil {
		return nil, err
	}
	if resp.Id != m.Id {
		return nil, dns.ErrId
	}
	return resp, co.SetDeadline(time.Time{})
}
