package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestStateFieldSet_RuntimeProjection(t *testing.T) {
	set := visorapi.NewStateFieldSet([]string{visorapi.SelectRuntime})
	require.True(t, set.Has(visorapi.SelectRuntime))
	require.False(t, set.Has(visorapi.SelectDiag))
	require.True(t, visorapi.NewStateFieldSet(nil).Has(visorapi.SelectRuntime), "runtime is in the default snapshot")
	require.Contains(t, visorapi.StateSelectKeys, visorapi.SelectRuntime)
}

func TestStateSnapshotRuntimeSection(t *testing.T) {
	snap, err := (&Visor{}).StateSnapshotProjected([]string{visorapi.SelectRuntime})
	require.NoError(t, err)
	require.NotNil(t, snap.Runtime)
	require.NotEmpty(t, snap.Runtime.Compiler)
	require.NotNil(t, snap.Runtime.HeapLiveBytes)
	require.Nil(t, snap.Diag, "--select runtime must build nothing else")
}
