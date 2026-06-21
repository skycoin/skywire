//go:build tinygo

// Package dmsg pkg/dmsg/quic_stub.go
//
// TinyGo build stubs for the transports that don't compile under TinyGo:
//
//   - dmsg-over-QUIC (quic-go needs crypto/tls.QUICEncryptionLevel, absent in
//     TinyGo's crypto/tls), and
//   - dmsg-over-WebTransport (pulls quic-go + webtransport-go).
//
// A TinyGo dmsg client never dials either — dialSession's switch only reaches
// these if a server advertises a QUIC / WebTransport endpoint, and the stubs'
// errors make it fall back to the TCP/WS path. The server-side listeners
// (ServeQUIC / ServeWebTransport) live only in their !tinygo files and are
// simply absent under TinyGo (a TinyGo build is a client, not a server). See
// docs/design/tinygo-dmsg-client.md.
package dmsg

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

var errNativeTransportUnsupported = errors.New("dmsg: QUIC/WebTransport not supported in this build (TinyGo); falling back to TCP/WS")

func (ce *Client) dialSessionQUIC(_ context.Context, _ *disc.Entry) (ClientSession, error) {
	return ClientSession{}, errNativeTransportUnsupported
}

func (ce *Client) dialSessionWT(_ context.Context, _ *disc.Entry) (ClientSession, error) {
	return ClientSession{}, errNativeTransportUnsupported
}
