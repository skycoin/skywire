package visor

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestTPDAnnounceStats(t *testing.T) {
	var s tpdAnnounceStats
	require.Nil(t, s.state(), "no announce yet: no section, unlike one whose announces fail")

	tpd, _ := cipher.GenerateKeyPair()
	s.record(tpd, errors.New("dial: no route"))
	st := s.state()
	require.Equal(t, int64(0), st.OK)
	require.Equal(t, int64(1), st.Failed)
	require.Equal(t, "dial: no route", st.LastError)
	require.Equal(t, -1.0, st.SecsSinceOK, "never succeeded")
	require.GreaterOrEqual(t, st.SecsSinceLastFail, 0.0)

	s.record(tpd, nil)
	st = s.state()
	require.Equal(t, int64(1), st.OK)
	require.GreaterOrEqual(t, st.SecsSinceOK, 0.0)
	require.Equal(t, tpd.Hex(), st.To)

	var none *tpdAnnounceStats
	none.record(tpd, nil) // a loop with no stats to keep must not panic
}
