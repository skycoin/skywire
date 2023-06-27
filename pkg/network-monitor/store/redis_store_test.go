//go:build !no_ci
// +build !no_ci

package store

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/nm"
)

func testNetwork(t *testing.T, store Store) {
	visorSumObj := make(map[cipher.PubKey]*nm.VisorSummary)

	const iterations = 3
	for i := 0; i < iterations; i++ {
		visorSummary := &nm.VisorSummary{
			Sudph:     true,
			Stcpr:     true,
			Timestamp: time.Now().Unix(),
		}
		pk, _ := cipher.GenerateKeyPair()
		visorSumObj[pk] = visorSummary
	}

	conn := context.Background()

	t.Run("add visor summaries", func(t *testing.T) {
		for pk, sum := range visorSumObj {
			err := store.AddVisorSummary(conn, pk, sum)
			require.NoError(t, err)
		}
	})

	t.Run("all summaries", func(t *testing.T) {
		summaries, err := store.GetAllSummaries()
		require.NoError(t, err)
		require.Len(t, summaries, 3)
	})

	t.Run("specific visor summary by pub key", func(t *testing.T) {
		for pk, sum := range visorSumObj {
			summarry, err := store.GetVisorByPk(pk.String())
			require.NoError(t, err)
			require.Equal(t, summarry, sum)
		}
	})

}
