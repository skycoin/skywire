// Package meshgw pkg/vpnrouter/meshgw/origdst_linux.go c4-app-vpn
//go:build linux
// +build linux

package meshgw

import (
	"encoding/binary"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// soOriginalDst is the SOL_IP option that returns the pre-REDIRECT destination
// of a connection (netfilter records it on the conntrack entry).
const soOriginalDst = 80

// originalDst returns the original destination (before iptables REDIRECT) of a
// transparently-proxied TCP connection, via getsockopt(SO_ORIGINAL_DST).
func originalDst(c *net.TCPConn) (net.IP, uint16, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, 0, err
	}
	var (
		ip     net.IP
		port   uint16
		optErr error
	)
	ctlErr := raw.Control(func(fd uintptr) {
		mreq, err := unix.GetsockoptIPv6Mreq(int(fd), unix.IPPROTO_IP, soOriginalDst)
		if err != nil {
			optErr = err
			return
		}
		// mreq.Multiaddr carries a sockaddr_in:
		//   [0:2] sin_family, [2:4] sin_port (big-endian), [4:8] sin_addr
		port = binary.BigEndian.Uint16(mreq.Multiaddr[2:4])
		ip = net.IPv4(mreq.Multiaddr[4], mreq.Multiaddr[5], mreq.Multiaddr[6], mreq.Multiaddr[7])
	})
	if ctlErr != nil {
		return nil, 0, ctlErr
	}
	if optErr != nil {
		return nil, 0, fmt.Errorf("getsockopt SO_ORIGINAL_DST: %w", optErr)
	}
	return ip, port, nil
}
