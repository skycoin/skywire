// Package meshgw pkg/vpnrouter/meshgw/dns.go c4-app-vpn
package meshgw

import (
	"github.com/miekg/dns"
)

// InstallDNS registers `.dmsg` and `.skynet` zone handlers on the router's DNS
// mux so queries for those names resolve to leased synthetic IPs. Everything
// else is left to the mux's existing handlers (the router's normal DNS).
func (g *Gateway) InstallDNS(mux *dns.ServeMux) {
	mux.HandleFunc("dmsg.", g.dnsHandler("dmsg"))
	mux.HandleFunc("skynet.", g.dnsHandler("skynet"))
}

func (g *Gateway) dnsHandler(scheme string) dns.HandlerFunc {
	return func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true

		if len(r.Question) != 1 {
			m.Rcode = dns.RcodeFormatError
			_ = w.WriteMsg(m) //nolint:errcheck // best-effort DNS reply
			return
		}
		q := r.Question[0]
		switch q.Qtype {
		case dns.TypeA:
			ip, err := g.resolve(scheme, normalizeName(q.Name))
			if err != nil {
				g.log.WithError(err).WithField("name", q.Name).Debug("mesh gateway: NXDOMAIN")
				m.Rcode = dns.RcodeNameError
				break
			}
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   ip,
			})
		case dns.TypeAAAA:
			// We only synthesize IPv4. Answer NOERROR/empty (not NXDOMAIN) so a
			// dual-stack resolver falls back to the A record instead of failing.
			if _, err := g.resolve(scheme, normalizeName(q.Name)); err != nil {
				m.Rcode = dns.RcodeNameError
			}
		default:
			// Any other type for a mesh name: NOERROR/empty.
		}
		_ = w.WriteMsg(m) //nolint:errcheck // best-effort DNS reply
	}
}
