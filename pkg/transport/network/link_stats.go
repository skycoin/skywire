// Package network pkg/transport/network/link_stats.go c2-net-transport
package network

import (
	"net"
	"time"

	kcp "github.com/0magnet/kcp-go/v5"
)

// LinkStats is a snapshot of the carrier's own RTT, congestion and loss
// state, read from the transport stack below skywire when requested.
type LinkStats struct {
	SRTTMs    float64 `json:"srtt_ms,omitempty"`
	MinRTTMs  float64 `json:"min_rtt_ms,omitempty"`
	RTOMs     float64 `json:"rto_ms,omitempty"`
	CwndBytes uint64  `json:"cwnd_bytes,omitempty"`
	// UnsentBytes is data queued in the kernel and not yet sent (TCP).
	UnsentBytes uint64 `json:"unsent_bytes,omitempty"`
	// Loss is nil when the carrier does not count losses (KCP, SCTP).
	Loss *LinkLoss `json:"loss,omitempty"`
}

// LinkLoss counts this side's sends only, because loss is detected by the
// sender. For TCP, lost means retransmitted.
type LinkLoss struct {
	PacketsSent uint64  `json:"packets_sent"`
	PacketsLost uint64  `json:"packets_lost"`
	BytesLost   uint64  `json:"bytes_lost"`
	Pct         float64 `json:"pct"`
}

func newLinkLoss(sent, lost, bytesLost uint64) *LinkLoss {
	l := &LinkLoss{PacketsSent: sent, PacketsLost: lost, BytesLost: bytesLost}
	if sent > 0 {
		l.Pct = float64(lost) * 100 / float64(sent)
	}
	return l
}

func durationMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// linkStats reads LinkStats from a raw conn whose type does not implement
// rawConnDetailer itself.
func linkStats(raw net.Conn) *LinkStats {
	if s, ok := raw.(*kcp.UDPSession); ok {
		return &LinkStats{SRTTMs: float64(s.GetSRTT()), RTOMs: float64(s.GetRTO())}
	}
	return tcpConnLinkStats(raw)
}
