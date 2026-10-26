// Package network pkg/transport/network/webrtc_announce_test.go
package network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateAddr(t *testing.T) {
	for c, want := range map[string]string{
		"candidate:1 1 udp 2122260223 203.0.113.7 54321 typ host":                                "203.0.113.7:54321",
		"candidate:2 1 udp 1686052607 2001:db8::7 3478 typ srflx raddr 0.0.0.0 rport 0":          "[2001:db8::7]:3478",
		"candidate:3 1 udp 41885439 198.51.100.9 61000 typ relay raddr 203.0.113.7 rport 54321":  "198.51.100.9:61000",
		"candidate:4 1 tcp 1518280447 203.0.113.7 9 typ host tcptype active":                     "203.0.113.7:9",
		"candidate:5 1 udp 2122260223 192.168.1.5 54321 typ host":                                "",
		"candidate:6 1 udp 2122260223 4b5d2f0c-7a8e-4c1d-9f3a-1b2c3d4e5f60.local 54321 typ host": "",
		"candidate:7 1 udp 2122260223 fe80::1 54321 typ host":                                    "",
		"not a candidate": "",
	} {
		got, ok := candidateAddr(c)
		require.Equal(t, want != "", ok, c)
		require.Equal(t, want, got, c)
	}
}

func TestPeerAddrsFromSDP(t *testing.T) {
	sdp := "v=0\r\na=candidate:1 1 udp 2122260223 203.0.113.7 54321 typ host\r\na=candidate:5 1 udp 2122260223 10.0.0.2 1 typ host\r\na=end-of-candidates\r\n"
	require.Equal(t, []string{"203.0.113.7:54321"}, peerAddrs(signalMsg{Type: "answer", SDP: sdp}))
	require.Empty(t, peerAddrs(signalMsg{Type: "candidate", Candidate: ""}))
}

func TestICEServerAddr(t *testing.T) {
	for u, want := range map[string]string{
		"stun:stun.skywire.dev:3478":        "stun.skywire.dev:3478",
		"stun:139.162.160.227":              "139.162.160.227:3478",
		"turn:198.51.100.9?transport=udp":   "198.51.100.9:3478",
		"turns:turn.example.org":            "turn.example.org:5349",
		"stun:[2001:db8::7]:3479":           "[2001:db8::7]:3479",
		"https://not-an-ice-server.example": "",
	} {
		got, ok := iceServerAddr(u)
		require.Equal(t, want != "", ok, u)
		require.Equal(t, want, got, u)
	}
}

// A browser visor's VPN host tunnel learns every WebRTC peer and ICE server
// before the first packet goes to it, so it can route them around itself.
func TestSignalConnAnnouncesPeers(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck

	var got []string
	rx := newSignalConn(b, []string{"stun:139.162.160.227:3478"}, func(addr string) { got = append(got, addr) })
	tx := newSignalConn(a, nil, nil)
	go func() {
		_ = tx.send(signalMsg{Type: "candidate", Candidate: "candidate:1 1 udp 2122260223 203.0.113.7 54321 typ host"}) //nolint:errcheck
	}()
	_, err := rx.recv()
	require.NoError(t, err)
	require.Equal(t, []string{"139.162.160.227:3478", "203.0.113.7:54321"}, got)
}
