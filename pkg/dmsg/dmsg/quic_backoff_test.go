package dmsg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestQUICShortSessionsBackOffToTCP pins the fallback that was missing when
// every QUIC dmsg session died 30 s after its handshake: two young deaths in
// a row put the server on TCP for quicBackoff; a session that lived clears
// the count; the backoff expires.
func TestQUICShortSessionsBackOffToTCP(t *testing.T) {
	ce := &Client{}
	ce.log = logging.MustGetLogger("quic_backoff_test")
	pk, _ := cipher.GenerateKeyPair()

	require.False(t, ce.quicBackedOff(pk), "nothing recorded yet")
	ce.noteQUICShortSession(pk)
	require.False(t, ce.quicBackedOff(pk), "one young death is not a pattern")
	ce.noteQUICShortSession(pk)
	require.True(t, ce.quicBackedOff(pk), "two in a row: dial TCP for a while")

	other, _ := cipher.GenerateKeyPair()
	ce.noteQUICShortSession(other)
	ce.noteQUICSessionOK(other)
	ce.noteQUICShortSession(other)
	require.False(t, ce.quicBackedOff(other), "a session that lived clears the strikes")

	old := quicBackoff
	quicBackoff = -time.Second
	defer func() { quicBackoff = old }()
	third, _ := cipher.GenerateKeyPair()
	ce.noteQUICShortSession(third)
	ce.noteQUICShortSession(third)
	require.False(t, ce.quicBackedOff(third), "an expired backoff is over")
}

// TestCanDialTCPTreatsEmptyCarriersAsDefault pins the fallback predicate: the
// native default (no carrier list) may fall back to TCP, an explicit list may
// only if it names tcp. hasCarrier(nil, tcp) alone said no, which left every
// default-configured client unable to leave a failed QUIC dial.
func TestCanDialTCPTreatsEmptyCarriersAsDefault(t *testing.T) {
	ce := &Client{}
	ce.conf = &Config{}
	require.True(t, ce.canDialTCP(), "empty carriers = native default = TCP allowed")
	ce.conf.Carriers = []string{CarrierQUIC}
	require.False(t, ce.canDialTCP(), "an explicit list without tcp stays off TCP")
	ce.conf.Carriers = []string{CarrierQUIC, CarrierTCP}
	require.True(t, ce.canDialTCP())
}
