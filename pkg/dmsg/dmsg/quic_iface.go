// Package dmsg pkg/dmsg/quic_iface.go
package dmsg

import (
	"context"
)

// quicConn / quicStream abstract the quic-go connection + stream that the
// session core uses, so the concrete *quic.Conn / *quic.Stream — and all of
// quic-go, which does NOT compile under TinyGo (it needs
// crypto/tls.QUICEncryptionLevel) — live only in quic_native.go (//go:build
// !tinygo). The core holds these interface types in SessionManager.quic and
// Stream.qStr; under TinyGo those fields are always nil, every
// `case sm.quic != nil` branch is dead, and a TinyGo dmsg client simply has no
// QUIC transport (the dial falls back to TCP/WS). See
// docs/design/tinygo-dmsg-client.md.

// quicConn is the subset of *quic.Conn the dmsg session core depends on.
type quicConn interface {
	OpenStreamSync(ctx context.Context) (quicStream, error)
	AcceptStream(ctx context.Context) (quicStream, error)
	SendDatagram(b []byte) error
	ReceiveDatagram(ctx context.Context) ([]byte, error)
	// CloseWithError mirrors *quic.Conn.CloseWithError with a plain uint64 code
	// (always 0 in dmsg) so the interface carries no quic-go types.
	CloseWithError(code uint64, msg string) error
}

// quicStream is the subset of *quic.Stream the dmsg session core depends on: a
// mux stream (Read/Write/Close/deadlines) plus its numeric id for logging.
type quicStream interface {
	muxStreamConn
	quicStreamID() uint32
}
