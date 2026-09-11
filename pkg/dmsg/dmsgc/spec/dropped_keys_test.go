// Package spec pkg/dmsg/dmsgc/spec/dropped_keys_test.go c1-net-dmsg
package spec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// DmsgConfig's codec is hand-written, so a visor-wide field carrying a json
// tag is NOT automatically (de)serialized — it has to be listed. direct_only
// and relay_max_streams were not, and were silently dropped both ways.
//
// relay_max_streams is the bound on how many streams this visor relays for
// others, i.e. the only configured limit on relay amplification. A knob that
// reads back as unset no matter what the operator wrote is worse than no knob.
func TestDmsgConfigKeepsVisorWideKeys(t *testing.T) {
	const raw = `{"discovery":"http://dmsgd","sessions_count":2,` +
		`"direct_only":true,"relay_max_streams":64,"lookup_cxo":true}`

	var c DmsgConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &c))
	require.True(t, c.DirectOnly, "direct_only must survive unmarshal")
	require.Equal(t, 64, c.RelayMaxStreams, "relay_max_streams must survive unmarshal")
	require.True(t, c.LookupCXO)

	out, err := json.Marshal(c)
	require.NoError(t, err)

	var back DmsgConfig
	require.NoError(t, json.Unmarshal(out, &back))
	require.True(t, back.DirectOnly, "direct_only must survive a round trip")
	require.Equal(t, 64, back.RelayMaxStreams, "relay_max_streams must survive a round trip")
	require.True(t, back.LookupCXO)
}

// The zero values stay absent, so a config that never set them does not grow
// the keys on rewrite.
func TestDmsgConfigOmitsUnsetVisorWideKeys(t *testing.T) {
	var c DmsgConfig
	require.NoError(t, json.Unmarshal([]byte(`{"discovery":"http://dmsgd"}`), &c))
	out, err := json.Marshal(c)
	require.NoError(t, err)
	require.NotContains(t, string(out), "direct_only")
	require.NotContains(t, string(out), "relay_max_streams")
}
