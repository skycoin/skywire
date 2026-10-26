//go:build linux || android

// Package network pkg/transport/network/link_stats_linux.go c2-net-transport
package network

import (
	"net"

	"golang.org/x/sys/unix"
)

// tcpLinkStats reads TCP_INFO (linux/include/uapi/linux/tcp.h, struct tcp_info).
func tcpLinkStats(c *net.TCPConn) *LinkStats {
	rc, err := c.SyscallConn()
	if err != nil {
		return nil
	}
	var ti *unix.TCPInfo
	if cerr := rc.Control(func(fd uintptr) {
		ti, err = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO) //nolint:gosec // fd is a small descriptor from RawConn.Control
	}); cerr != nil || err != nil {
		return nil
	}
	return &LinkStats{
		SRTTMs:      float64(ti.Rtt) / 1000,
		MinRTTMs:    float64(ti.Min_rtt) / 1000,
		RTOMs:       float64(ti.Rto) / 1000,
		CwndBytes:   uint64(ti.Snd_cwnd) * uint64(ti.Snd_mss),
		UnsentBytes: uint64(ti.Notsent_bytes),
		Loss:        newLinkLoss(uint64(ti.Segs_out), uint64(ti.Total_retrans), ti.Bytes_retrans),
	}
}
