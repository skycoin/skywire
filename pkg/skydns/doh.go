// Package skydns pkg/skydns/doh.go
package skydns

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/miekg/dns"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"

	"github.com/skycoin/skywire/pkg/skynetca"
)

// DoHURL is the DNS over HTTPS template to give a browser whose own secure
// DNS would otherwise bypass SkyDNS. It is served when a CA is configured.
const DoHURL = "https://" + ResolverAddr + dohPath

const (
	dohPath    = "/dns-query"
	dohType    = "application/dns-message"
	maxDoHSize = dns.MaxMsgSize
)

// serveDoH answers RFC 8484 queries on ResolverAddr:443 with the same
// handlers as port 53, under a leaf minted for the resolver's IP.
func (e *Engine) serveDoH(h dns.Handler, minter skynetca.IPMinter) error {
	ip := netip.MustParseAddr(ResolverAddr)
	at := tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(ip.As4()), Port: 443}
	ln, err := gonet.ListenTCP(e.stack, at, ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("skydns: doh listen: %w", err)
	}
	e.doh = &http.Server{
		Handler: dohHandler(h),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return minter.ForIP(ip)
			},
		},
		ReadHeaderTimeout: queryTimeout,
	}
	go func() {
		if err := e.doh.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) && e.ctx.Err() == nil {
			e.log.WithError(err).Warn("skydns: doh stopped")
		}
	}()
	return nil
}

func dohHandler(h dns.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != dohPath {
			http.NotFound(w, r)
			return
		}
		var raw []byte
		switch r.Method {
		case http.MethodGet:
			b, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("dns"))
			if err != nil || len(b) == 0 {
				http.Error(w, "missing or malformed dns parameter", http.StatusBadRequest)
				return
			}
			raw = b
		case http.MethodPost:
			if r.Header.Get("Content-Type") != dohType {
				http.Error(w, "want "+dohType, http.StatusUnsupportedMediaType)
				return
			}
			b, err := io.ReadAll(io.LimitReader(r.Body, maxDoHSize+1))
			if err != nil || len(b) > maxDoHSize {
				http.Error(w, "unreadable or oversized query", http.StatusRequestEntityTooLarge)
				return
			}
			raw = b
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := new(dns.Msg)
		if err := q.Unpack(raw); err != nil {
			http.Error(w, "malformed dns message", http.StatusBadRequest)
			return
		}
		rw := &dohWriter{remote: r.RemoteAddr}
		h.ServeDNS(rw, q)
		if rw.resp == nil {
			http.Error(w, "no answer", http.StatusBadGateway)
			return
		}
		out, err := rw.resp.Pack()
		if err != nil {
			http.Error(w, "unpackable answer", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", dohType)
		// RFC 8484 section 5.1: a response is fresh no longer than its smallest TTL.
		if ttl, ok := minTTL(rw.resp); ok {
			w.Header().Set("Cache-Control", "max-age="+strconv.FormatUint(uint64(ttl), 10))
		}
		_, _ = w.Write(out) //nolint:errcheck
	})
}

func minTTL(m *dns.Msg) (uint32, bool) {
	var ttl uint32
	found := false
	for _, sec := range [][]dns.RR{m.Answer, m.Ns} {
		for _, rr := range sec {
			if t := rr.Header().Ttl; !found || t < ttl {
				ttl, found = t, true
			}
		}
	}
	return ttl, found
}

// dohWriter holds the one reply a handler writes, which the DoH handler then
// returns as the HTTP body. Its addresses are TCP so no reply is truncated.
type dohWriter struct {
	remote string
	resp   *dns.Msg
}

func (w *dohWriter) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP(ResolverAddr), Port: 443}
}

func (w *dohWriter) RemoteAddr() net.Addr {
	if ap, err := netip.ParseAddrPort(w.remote); err == nil {
		return net.TCPAddrFromAddrPort(ap)
	}
	return &net.TCPAddr{}
}

func (w *dohWriter) WriteMsg(m *dns.Msg) error {
	w.resp = m.Copy()
	return nil
}

func (w *dohWriter) Write(b []byte) (int, error) {
	m := new(dns.Msg)
	if err := m.Unpack(b); err != nil {
		return 0, err
	}
	w.resp = m
	return len(b), nil
}

func (w *dohWriter) Close() error        { return nil }
func (w *dohWriter) TsigStatus() error   { return nil }
func (w *dohWriter) TsigTimersOnly(bool) {}
func (w *dohWriter) Hijack()             {}
