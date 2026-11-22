// Package clirewards cmd/skywire-cli/commands/rewards/bwcollect_stream_test.go c4-vis-cli
package clirewards

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEachTPDTransportAgreesWithUnmarshal checks the streaming decoder
// accepts and refuses the same bodies as json.Unmarshal into a slice.
func TestEachTPDTransportAgreesWithUnmarshal(t *testing.T) {
	good := `[{"id":"tp-1","type":"stcpr","edges":["a","b"],"daily":[{"date":"2026-11-21","a":{"sent":5,"recv":1},"b":{"sent":1,"recv":5}}]},{"id":"tp-2"}]`
	for name, body := range map[string]string{
		"good":          good,
		"null":          "null",
		"empty":         "[]",
		"spaces":        " \n[ ] ",
		"object":        `{"id":"tp-1"}`,
		"trailing data": good + " []",
		"truncated":     good[:len(good)-1],
		"wrong type":    `[{"id":5}]`,
		"string":        `["x"]`,
		"nothing":       "",
	} {
		var want []tpdTransport
		wantErr := json.Unmarshal([]byte(body), &want)
		var got []tpdTransport
		n, gotErr := eachTPDTransport([]byte(body), func(tp *tpdTransport) { got = append(got, *tp) })
		require.Equal(t, wantErr == nil, gotErr == nil, name)
		if wantErr == nil {
			require.Equal(t, len(want), n, name)
			if n > 0 {
				require.Equal(t, want, got, name)
			}
		}
	}
}
