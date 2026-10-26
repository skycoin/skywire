package transport

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestDialRecorderCountsAndKeepsFailures(t *testing.T) {
	var r dialRecorder
	pk, _ := cipher.GenerateKeyPair()
	start := time.Now().Add(-2 * time.Second)

	r.record("sudph", pk, LabelAutomatic, start, errors.New("handshake failed: timeout"))
	r.record("sudph", pk, LabelAutomatic, start, nil)
	r.record("stcpr", pk, LabelUser, start, nil)

	stats, fails := r.snapshot()
	require.Equal(t, DialStat{Attempts: 2, OK: 1, Failed: 1, LastError: "handshake failed: timeout", LastErrorAt: stats["sudph"].LastErrorAt}, stats["sudph"])
	require.Equal(t, uint64(1), stats["stcpr"].OK)
	require.Len(t, fails, 1)
	require.Equal(t, "sudph", fails[0].Type)
	require.Equal(t, pk, fails[0].Remote)
	require.GreaterOrEqual(t, fails[0].DurS, 2.0)
}

func TestDialFailureRingIsBoundedAndOrdered(t *testing.T) {
	var r dialRecorder
	for i := 0; i < DialFailureRingSize+5; i++ {
		r.record("sudph", cipher.PubKey{}, LabelAutomatic, time.Now(), errors.New(string(rune('a'+i%26))))
	}
	stats, fails := r.snapshot()
	require.Len(t, fails, DialFailureRingSize)
	require.Equal(t, uint64(DialFailureRingSize+5), stats["sudph"].Failed)
	require.Equal(t, string(rune('a'+5%26)), fails[0].Error, "oldest kept is the sixth recorded")
	require.Equal(t, fails[len(fails)-1].Error, stats["sudph"].LastError)
}
