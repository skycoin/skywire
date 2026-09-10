// Package api pkg/deployment/ar/api/sudph_packet_log_test.go c4-net-discovery
package api

import (
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// packetLogHook records every entry the logger emits.
type packetLogHook struct{ entries []*logrus.Entry }

func (h *packetLogHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *packetLogHook) Fire(e *logrus.Entry) error {
	h.entries = append(h.entries, e)
	return nil
}

func packetLogger() (*logrus.Logger, *packetLogHook) {
	l := logrus.New()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.TraceLevel)
	h := &packetLogHook{}
	l.AddHook(h)
	return l, h
}

// The per-packet SUDPH log costs nothing when the level is off. It used to
// format string(data) at the call site, copying every datagram the address
// resolver received whether or not the line was ever printed.
func TestLogSUDPHPacketCostsNothingWhenOff(t *testing.T) {
	old := logging.GetLevel()
	defer logging.SetLevel(old)

	l, h := packetLogger()
	pk, _ := cipher.GenerateKeyPair()
	payload := []byte("SECRET-PAYLOAD-do-not-log")

	logging.SetLevel(logrus.DebugLevel)
	logSUDPHPacket(l, pk, "1.2.3.4:5", payload)
	require.Empty(t, h.entries, "a per-packet line must not be emitted below trace")

	allocs := testing.AllocsPerRun(100, func() {
		logSUDPHPacket(l, pk, "1.2.3.4:5", payload)
	})
	require.Zero(t, allocs, "the disabled per-packet log must not allocate, let alone copy the payload")
}

// When the level is on, the line reports the length and never the bytes: the
// payload is either a control word the caller already logs or a visor's
// local-address JSON, and it may carry whatever a peer chose to send.
func TestLogSUDPHPacketOmitsPayload(t *testing.T) {
	old := logging.GetLevel()
	defer logging.SetLevel(old)
	logging.SetLevel(logrus.TraceLevel)

	l, h := packetLogger()
	pk, _ := cipher.GenerateKeyPair()
	payload := []byte("SECRET-PAYLOAD-do-not-log")

	logSUDPHPacket(l, pk, "1.2.3.4:5", payload)

	require.Len(t, h.entries, 1)
	e := h.entries[0]
	require.Equal(t, logrus.TraceLevel, e.Level, "per-packet lines belong below debug")
	require.NotContains(t, e.Message, "SECRET-PAYLOAD", "the payload must never reach the log")
	require.Contains(t, e.Message, "25 bytes")
	require.Contains(t, e.Message, pk.String())
}
