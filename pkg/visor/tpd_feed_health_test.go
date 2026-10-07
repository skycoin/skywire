package visor

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestTPDAnnounceAnsweredWithin(t *testing.T) {
	var s tpdAnnounceStats
	require.False(t, s.answeredWithin(time.Minute), "never announced")
	s.record(cipher.PubKey{}, errors.New("down"))
	require.False(t, s.answeredWithin(time.Minute), "only failures")
	s.record(cipher.PubKey{}, nil)
	require.True(t, s.answeredWithin(time.Minute))
	s.lastOK = time.Now().Add(-2 * time.Minute)
	require.False(t, s.answeredWithin(time.Minute), "an old answer does not count")
}

func TestTPDFeedHealthyWithoutFeed(t *testing.T) {
	require.False(t, (&Visor{}).tpdFeedHealthy(), "no feed means the HTTP heartbeat")
}
