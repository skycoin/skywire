package vpn

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// ipv4Packet builds just enough of an IPv4 header plus the first four bytes
// of a transport header for isSharedPacket to judge.
func ipv4Packet(dst [4]byte, proto byte, dport uint16, fragOffset uint16) []byte {
	p := make([]byte, 28)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[6:8], fragOffset)
	p[9] = proto
	copy(p[16:20], dst[:])
	binary.BigEndian.PutUint16(p[22:24], dport)
	return p
}

func TestIsSharedPacket(t *testing.T) {
	ip := [4]byte{10, 8, 0, 4}
	other := [4]byte{10, 8, 0, 5}

	cases := []struct {
		name string
		p    []byte
		want bool
	}{
		{"tcp reply to the netstack", ipv4Packet(ip, 6, shareFirstPort, 0), true},
		{"udp reply to the netstack", ipv4Packet(ip, 17, shareLastPort, 0), true},
		{"tcp to the kernel's range", ipv4Packet(ip, 6, shareFirstPort-1, 0), false},
		{"another address", ipv4Packet(other, 6, shareFirstPort, 0), false},
		{"icmp", ipv4Packet(ip, 1, shareFirstPort, 0), false},
		{"later fragment", ipv4Packet(ip, 17, shareFirstPort, 185), false},
		{"first fragment", ipv4Packet(ip, 17, shareFirstPort, 0x2000), true},
		{"ipv6", append([]byte{0x60}, make([]byte, 39)...), false},
		{"truncated", ipv4Packet(ip, 6, shareFirstPort, 0)[:21], false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isSharedPacket(tc.p, ip))
		})
	}
}

// countingWriter stands in for the phone's TUN: what the demux hands the
// kernel lands here.
type countingWriter struct{ n atomic.Int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n.Add(1)
	return len(p), nil
}

// shareRig is a tunnel session with a far end: a netstack standing in for the
// VPN server and the internet behind it, carrying an HTTP site and a resolver
// at remoteIP. The client's shared netstack and "kernel" share tunnelIP, as
// they do on the phone.
type shareRig struct {
	client *Client
	sess   *shareSession
	kernel *countingWriter
}

var (
	shareTunnelIP = net.IPv4(10, 8, 0, 4)
	shareRemoteIP = [4]byte{1, 2, 3, 4}
)

const shareTestHost = "site.test"

func newShareRig(t *testing.T) *shareRig {
	t.Helper()

	remote, err := newNetstackTUN()
	require.NoError(t, err)
	t.Cleanup(func() { _ = remote.Close() }) //nolint:errcheck
	require.NoError(t, remote.configure("1.2.3.4/24"))

	c := &Client{cfg: ClientConfig{DNSAddr: "1.2.3.4"}, sharing: true}
	kernel := &countingWriter{}
	sess := newShareSession(shareTunnelIP, remote.Write)
	c.share.Store(sess)
	t.Cleanup(sess.close)

	// The server's replies, as the serve loop's inbound copy delivers them.
	demux := shareDemux{tun: kernel, sess: sess}
	go func() {
		buf := make([]byte, TUNMTU+4)
		for {
			n, err := remote.Read(buf)
			if err != nil {
				return
			}
			_, _ = demux.Write(buf[:n]) //nolint:errcheck
		}
	}()

	serveRemoteHTTP(t, remote)
	serveRemoteDNS(t, remote)

	return &shareRig{client: c, sess: sess, kernel: kernel}
}

func serveRemoteHTTP(t *testing.T, remote *netstackTUN) {
	t.Helper()
	l, err := gonet.ListenTCP(remote.stack, tcpip.FullAddress{
		NIC: netstackNICID, Addr: tcpip.AddrFrom4(shareRemoteIP), Port: 80,
	}, ipv4.ProtocolNumber)
	require.NoError(t, err)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello from "+r.Host+r.URL.Path) //nolint:errcheck,gosec // test fixture echoes its own request
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go srv.Serve(l)                       //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck
}

// serveRemoteDNS answers A queries for shareTestHost with the remote address.
func serveRemoteDNS(t *testing.T, remote *netstackTUN) {
	t.Helper()
	pc, err := gonet.DialUDP(remote.stack, &tcpip.FullAddress{
		NIC: netstackNICID, Addr: tcpip.AddrFrom4(shareRemoteIP), Port: 53,
	}, nil, ipv4.ProtocolNumber)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() }) //nolint:errcheck
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var msg dnsmessage.Message
			if err := msg.Unpack(buf[:n]); err != nil || len(msg.Questions) == 0 {
				continue
			}
			q := msg.Questions[0]
			msg.Header.Response = true
			msg.Header.RecursionAvailable = true
			if q.Type == dnsmessage.TypeA && q.Name.String() == shareTestHost+"." {
				msg.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: shareRemoteIP},
				}}
			} else if q.Type != dnsmessage.TypeA {
				msg.Answers = nil
			} else {
				msg.Header.RCode = dnsmessage.RCodeNameError
			}
			out, err := msg.Pack()
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(out, from) //nolint:errcheck
		}
	}()
}

// proxyClient serves the rig's proxy on a loopback listener and returns an
// HTTP client that goes through it with proxyScheme.
func (r *shareRig) proxyClient(t *testing.T, proxyScheme string) *http.Client {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go r.client.serveShared(l)          //nolint:errcheck

	proxy := &url.URL{Scheme: proxyScheme, Host: l.Addr().String()}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true},
		Timeout:   15 * time.Second,
	}
}

func get(t *testing.T, hc *http.Client, target string) (int, string) {
	t.Helper()
	resp, err := hc.Get(target)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// TestShareProxyThroughTunnel is the hotspot's whole data path minus the app:
// a shared client's HTTP proxy request and SOCKS5 connect (by name, so the
// resolver rides the tunnel too) reach the far end through the shared
// netstack, and none of the replies is handed to the phone's kernel.
func TestShareProxyThroughTunnel(t *testing.T) {
	rig := newShareRig(t)

	code, body := get(t, rig.proxyClient(t, "http"), "http://1.2.3.4/over-http")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "hello from 1.2.3.4/over-http", body)

	code, body = get(t, rig.proxyClient(t, "socks5"), "http://"+shareTestHost+"/over-socks")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "hello from "+shareTestHost+"/over-socks", body)

	require.Zero(t, rig.kernel.n.Load(), "a reply to the shared netstack reached the phone's kernel")
}

// TestShareWithoutTunnel: between sessions a shared connection fails instead
// of finding another way out.
func TestShareWithoutTunnel(t *testing.T) {
	rig := newShareRig(t)
	hc := rig.proxyClient(t, "http")

	rig.sess.close()
	code, _ := get(t, hc, "http://1.2.3.4/")
	require.Equal(t, http.StatusBadGateway, code)

	rig.client.share.Store(nil)
	_, err := rig.client.shareDial(context.Background(), "tcp", "1.2.3.4:80")
	require.ErrorIs(t, err, errShareNoTunnel)
}

func TestShareRefusesLocalAddresses(t *testing.T) {
	rig := newShareRig(t)
	for _, addr := range []string{"127.0.0.1:80", "0.0.0.0:80", "224.0.0.1:80", "[::1]:80"} {
		_, err := rig.client.shareDial(context.Background(), "tcp", addr)
		require.ErrorIs(t, err, errShareLocal, addr)
	}
}

// TestShareDemuxKeepsKernelTraffic: with the netstack up, a packet for the
// kernel's port range still goes to the kernel.
func TestShareDemuxKeepsKernelTraffic(t *testing.T) {
	rig := newShareRig(t)
	_, err := rig.sess.netstack()
	require.NoError(t, err)

	demux := shareDemux{tun: rig.kernel, sess: rig.sess}
	var ip [4]byte
	copy(ip[:], shareTunnelIP.To4())
	_, err = demux.Write(ipv4Packet(ip, 6, 40000, 0))
	require.NoError(t, err)
	require.EqualValues(t, 1, rig.kernel.n.Load())
}
