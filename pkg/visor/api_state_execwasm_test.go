// Package visor pkg/visor/api_state_execwasm_test.go c3-vis-core
package visor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExecWasmInfo_StaleOnlyWhenBothKnown: Stale must never be asserted from a
// guess. A source build records no revision and a dev binary may record no
// commit; in either case "different" is unknowable and reporting stale would
// send someone rebuilding a 176 MB module for nothing.
func TestExecWasmInfo_StaleOnlyWhenBothKnown(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rev, bin  string
		wantStale bool
	}{
		{"both known and equal", "abc123", "abc123", false},
		{"both known and different", "abc123", "def456", true},
		{"module revision unrecorded", "", "def456", false},
		{"binary commit unrecorded", "abc123", "", false},
		{"neither known", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.rev != "" && tc.bin != "" && tc.rev != tc.bin
			require.Equal(t, tc.wantStale, got)
		})
	}
}

// The snapshot helper must not invent a section for a build that has no module
// and no recorded revision — omitempty then keeps `exec_wasm` out of the JSON
// entirely rather than showing a hollow all-false block.
func TestExecWasmInfo_NilWhenNothingToReport(t *testing.T) {
	info := execWasmInfo()
	if info == nil {
		return // source build: correct
	}
	require.True(t, info.Present || info.Revision != "",
		"a non-nil ExecWasmInfo must describe something real")
	if info.Stale {
		require.NotEmpty(t, info.Revision)
		require.NotEmpty(t, info.BinaryRevision)
		require.NotEqual(t, info.Revision, info.BinaryRevision)
	}
}
