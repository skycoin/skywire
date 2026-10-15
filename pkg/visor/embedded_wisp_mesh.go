// Package visor pkg/visor/embedded_wisp_mesh.go c3-vis-core
//
// The embedded Wisp server reaches .dmsg and .skynet names over the mesh, the
// way SkyDNS does for a phone. A guest resolves a name before it connects, so
// DNS is answered here too: any TCP stream to port 53 gets mesh names answered
// with a synthetic address and every other query passed to the server it
// asked, through the exit.
package visor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/0magnet/wisp"
	"github.com/miekg/dns"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// wispMeshCIDR is SkyDNS's pool (pkg/skydns PoolCIDR), so a mesh name has the
// same kind of address in a tab as on a phone.
const wispMeshCIDR = "198.18.0.0/16"

// meshEgress sends mesh names over the mesh and everything else to next.
type meshEgress struct {
	next wisp.Egress
	gw   *meshgw.Gateway
}

// meshScheme is "dmsg" or "skynet" for a mesh name, else "".
func meshScheme(host string) string {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	switch {
	case strings.HasSuffix(h, ".dmsg"):
		return "dmsg"
	case strings.HasSuffix(h, ".skynet"):
		return "skynet"
	}
	return ""
}

// DialTCP implements wisp.Egress.
func (e *meshEgress) DialTCP(ctx context.Context, host string, port uint16) (net.Conn, error) {
	if port == 53 {
		c, s := net.Pipe()
		go e.serveDNS(ctx, s, host)
		return c, nil
	}
	if scheme := meshScheme(host); scheme != "" {
		ip, err := e.gw.Resolve(scheme, host)
		if err != nil {
			return nil, err
		}
		return e.gw.DialIP(ctx, ip, port)
	}
	if ip := net.ParseIP(host); ip != nil && e.gw.PoolCIDR().Contains(ip) {
		return e.gw.DialIP(ctx, ip, port)
	}
	return e.next.DialTCP(ctx, host, port)
}

// DialUDP implements wisp.Egress.
func (e *meshEgress) DialUDP(ctx context.Context, host string, port uint16) (wisp.DatagramStream, error) {
	return e.next.DialUDP(ctx, host, port)
}

// Describe implements wisp.Egress.
func (e *meshEgress) Describe() string {
	return e.next.Describe() + ", .dmsg and .skynet over the mesh"
}

// serveDNS answers DNS over TCP (RFC 1035 4.2.2, two-byte length frames) on
// conn. server is the resolver the guest asked, which gets every query that
// is not for a mesh name.
func (e *meshEgress) serveDNS(ctx context.Context, conn net.Conn, server string) {
	defer conn.Close() //nolint:errcheck
	var up net.Conn
	defer func() {
		if up != nil {
			_ = up.Close() //nolint:errcheck
		}
	}()
	for {
		q, err := readDNSFrame(conn)
		if err != nil {
			return
		}
		ans, err := e.answerMesh(q)
		if errors.Is(err, errNotMesh) {
			if up == nil {
				if up, err = e.next.DialTCP(ctx, server, 53); err != nil {
					return
				}
			}
			if err = writeDNSFrame(up, q); err == nil {
				ans, err = readDNSFrame(up)
			}
		}
		if err != nil || writeDNSFrame(conn, ans) != nil {
			return
		}
	}
}

var errNotMesh = errors.New("not a mesh name")

// answerMesh answers a query for a mesh name, or returns errNotMesh.
func (e *meshEgress) answerMesh(raw []byte) ([]byte, error) {
	var q dns.Msg
	if err := q.Unpack(raw); err != nil || len(q.Question) != 1 {
		return nil, errNotMesh
	}
	question := q.Question[0]
	scheme := meshScheme(question.Name)
	if scheme == "" {
		return nil, errNotMesh
	}
	m := new(dns.Msg)
	m.SetReply(&q)
	m.Authoritative = true
	if question.Qtype == dns.TypeA {
		ip, err := e.gw.Resolve(scheme, question.Name)
		if err != nil {
			m.Rcode = dns.RcodeNameError
		} else {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   ip,
			})
		}
	}
	return m.Pack()
}

func readDNSFrame(r io.Reader) ([]byte, error) {
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(n[:]))
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeDNSFrame(w io.Writer, b []byte) error {
	if len(b) > 0xffff {
		return fmt.Errorf("dns message of %d bytes", len(b))
	}
	f := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(f, uint16(len(b))) //nolint:gosec // checked above
	copy(f[2:], b)
	_, err := w.Write(f)
	return err
}

// wispMeshDial is the visor's dial for the mesh gateway: a .dmsg name as the
// resolving proxy reaches it, a .skynet name as the desk browser does.
func (v *Visor) wispMeshDial(ctx context.Context, scheme string, pk cipher.PubKey, port uint16) (net.Conn, error) {
	switch scheme {
	case "dmsg":
		if v.dmsgC == nil {
			return nil, errors.New("dmsg: not up")
		}
		return v.dialDmsgHTTP(ctx, "tcp", net.JoinHostPort(pk.Hex(), strconv.Itoa(int(port))))
	case "skynet":
		if v.router == nil {
			return nil, errors.New("skynet: not up")
		}
		d := &routerSkynetDialer{router: v.router, localPK: v.conf.PK, log: v.log, tpM: v.tpM, skynetMuxPtr: &v.skynetFwdMux}
		return d.DialSkynet(ctx, pk, port, nil)
	}
	return nil, fmt.Errorf("unknown mesh scheme %q", scheme)
}
