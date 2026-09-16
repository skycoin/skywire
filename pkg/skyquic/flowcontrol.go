// Package skyquic pkg/skyquic/flowcontrol.go c1-net-transport
//
// Shared QUIC flow-control windows for every skywire QUIC-based carrier: the
// skynet QUIC transport (squicr, pkg/transport/network), dmsg-over-QUIC
// (pkg/dmsg/dmsg) and the WebTransport carriers that ride the same sockets.
// They live here, next to the shared TLS identity, because both packages build
// their own quic.Config and pkg/dmsg cannot import pkg/transport/network back.
//
// Why they have to be set explicitly: quic-go's defaults are a 512 KB initial
// stream window and a 768 KB initial CONNECTION window, and the receiver's
// window is the whole in-flight budget of a BDP-bound path. On a 150 ms-RTT
// link that caps one connection at
//
//	768 KiB / 0.150 s ≈ 5.1 MB/s
//
// which is exactly the throughput a squicr transport measured at before these
// were set. quic-go does auto-tune the window upward, but only while the
// receiver drains at least half a window inside ~4 RTT; a receive chain that
// hands frames to an application through a buffered channel does not reliably
// meet that, so the connection sits at the initial value indefinitely. One
// skywire transport is one QUIC connection carrying one stream, so the
// connection window is that transport's entire budget.
//
// The values below start at ~8 MiB/stream and 12 MiB/connection (≈ 80 MB/s at
// 150 ms RTT, so the window is no longer the limit on any path we serve) and
// let auto-tuning grow to 64/96 MiB for longer or fatter paths. They are
// receive-side buffer CEILINGS, not allocations: quic-go grows the actual
// buffer on demand, so an idle or slow connection does not pay for them.
package skyquic

const (
	// InitialStreamReceiveWindow is the per-stream receive window a QUIC
	// connection starts with (quic-go default: 512 KB).
	InitialStreamReceiveWindow = 8 << 20 // 8 MiB
	// MaxStreamReceiveWindow bounds per-stream auto-tuning (default: 6 MB).
	MaxStreamReceiveWindow = 64 << 20 // 64 MiB
	// InitialConnectionReceiveWindow is the whole-connection receive window a
	// QUIC connection starts with (quic-go default: 768 KB — the cap this
	// fixes).
	InitialConnectionReceiveWindow = 12 << 20 // 12 MiB
	// MaxConnectionReceiveWindow bounds whole-connection auto-tuning
	// (default: 15 MB).
	MaxConnectionReceiveWindow = 96 << 20 // 96 MiB
)
