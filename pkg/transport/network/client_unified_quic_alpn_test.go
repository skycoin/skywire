package network

import (
	"crypto/tls"
	"testing"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyquic"
)

// TestSharedQUICMuxRefusesDmsgALPN pins the other half of the ALPN split: a
// transport-port socket with only squicr registered refuses a dmsg client at
// the handshake instead of accepting it and holding the session for 30 s
// waiting for a stream. The refusal is what makes the client fall back to TCP.
func TestSharedQUICMuxRefusesDmsgALPN(t *testing.T) {
	m := &sharedQUICMux{tlsByAL: map[string]*tls.Config{quicALPN: {}}, handler: map[string]func(*quic.Conn){}}
	_, err := m.getConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{skyquic.DmsgNextProto}})
	require.Error(t, err, "a dmsg-only hello has no handler here")
	cfg, err := m.getConfigForClient(&tls.ClientHelloInfo{SupportedProtos: []string{quicALPN}})
	require.NoError(t, err)
	require.Equal(t, []string{quicALPN}, cfg.NextProtos)
}
