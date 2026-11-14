//go:build !mobile && !js && !tinygo

package visor

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// The reward service runs under its own key from a keyfile, shows in visor
// state, and is started again with backoff after it stops.
func TestEmbeddedRewardsService(t *testing.T) {
	ownKeyMinBackoff = 200 * time.Millisecond
	t.Cleanup(func() { ownKeyMinBackoff = 5 * time.Second })

	keyfile := filepath.Join(t.TempDir(), "rewards.key")
	raw, err := json.Marshal(map[string]any{"type": "rewards", "name": "rewards", "keyfile": keyfile,
		"work_dir": filepath.Join(t.TempDir(), "missing"), "no_cycle": true})
	require.NoError(t, err)
	var b services.Block
	require.NoError(t, json.Unmarshal(raw, &b))
	visorPK, visorSK := cipher.GenerateKeyPair()
	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: []services.Block{b}}}
	require.NoError(t, initOwnKeyServices(t.Context(), v, logging.MustGetLogger("test")))

	// A failed start is reported until the backoff ends and the next start clears it.
	require.Eventually(t, func() bool {
		st := v.embeddedServiceStates()
		return len(st) == 1 && strings.Contains(st[0].Error, "work_dir")
	}, 5*time.Second, 5*time.Millisecond)
	st := v.embeddedServiceStates()[0]
	require.Equal(t, "rewards", st.Type)
	require.True(t, st.OwnKey)
	require.NotEmpty(t, st.URL)
	require.Eventually(t, func() bool { return v.embeddedServiceStates()[0].Restarts >= 1 }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, v.EmbeddedServiceControl("rewards", "stop"))
	require.Eventually(t, func() bool { return v.embeddedServiceStates()[0].Stopped }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, v.EmbeddedServiceControl("rewards", "start"))
	require.Eventually(t, func() bool { return !v.embeddedServiceStates()[0].Stopped }, 5*time.Second, 10*time.Millisecond)
}
