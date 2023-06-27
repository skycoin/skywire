//go:build !no_ci
// +build !no_ci

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/internal/lc"
)

func testNetwork(t *testing.T, store Store) {
	serviceSumObj := make(map[string]*lc.ServiceSummary)

	const iterations = 3
	for i := 0; i < iterations; i++ {
		serviceSummary := &lc.ServiceSummary{
			Online:    true,
			Health:    &httputil.HealthCheckResponse{},
			Timestamp: time.Now().Unix(),
		}
		serviceSumObj["dmsgd"] = serviceSummary
	}

	conn := context.Background()

	t.Run("add service summaries", func(t *testing.T) {
		for pk, sum := range serviceSumObj {
			err := store.AddServiceSummary(conn, pk, sum)
			require.NoError(t, err)
		}
	})
}
