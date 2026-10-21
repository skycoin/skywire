package visor

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The streamed bodies must be byte for byte what httputil.WriteJSON wrote.
func encoderBytes(t *testing.T, v interface{}) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(v))
	return buf.Bytes()
}

func testSummaries() []visorapi.Summary {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return []visorapi.Summary{
		{Overview: &visorapi.Overview{PubKey: pk1}, BuildTag: "<b>&tag</b>", IsPublic: true},
		{Overview: &visorapi.Overview{PubKey: pk2}, RewardAddress: "addr", MinHops: 2},
	}
}

func TestWriteVisorTreeMatchesEncoder(t *testing.T) {
	hv1, _ := cipher.GenerateKeyPair()
	hv2, _ := cipher.GenerateKeyPair()
	via, _ := cipher.GenerateKeyPair()
	cases := map[string]VisorTreeResponse{
		"nil sections":   {},
		"empty sections": {Sections: []VisorTreeSection{}},
		"full": {Sections: []VisorTreeSection{
			{HypervisorPK: hv1, Visors: testSummaries()},
			{HypervisorPK: hv2, ViaChain: []cipher.PubKey{via}, Visors: []visorapi.Summary{}, SubError: `timed out <"x">`},
			{HypervisorPK: hv2, Visors: nil},
		}},
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, writeVisorTree(&buf, resp))
			buf.WriteByte('\n')
			require.Equal(t, string(encoderBytes(t, resp)), buf.String())
		})
	}
}

func TestWriteJSONArrayMatchesEncoder(t *testing.T) {
	for name, items := range map[string][]visorapi.Summary{
		"nil":   nil,
		"empty": {},
		"two":   testSummaries(),
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, writeJSONArray(&buf, items))
			buf.WriteByte('\n')
			require.Equal(t, string(encoderBytes(t, items)), buf.String())
		})
	}
}
