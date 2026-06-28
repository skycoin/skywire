package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func TestIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	assert.False(t, isDone(ctx))
	cancel()
	assert.True(t, isDone(ctx))
}

func TestIsRetryableDialErr(t *testing.T) {
	assert.False(t, isRetryableDialErr(nil))
	assert.True(t, isRetryableDialErr(dmsg.ErrCannotConnectToDelegated))
	// Sentinel lost across the wire → message fallback.
	assert.True(t, isRetryableDialErr(errors.New("dial hop: cannot connect to delegated server")))
	assert.False(t, isRetryableDialErr(errors.New("some unrelated failure")))
}

func TestDialHopWithRetry(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("success first try", func(t *testing.T) {
		want := &Client{}
		got, err := dialHopWithRetry(context.Background(), pk,
			func(context.Context, cipher.PubKey) (*Client, error) { return want, nil })
		require.NoError(t, err)
		assert.Same(t, want, got)
	})

	t.Run("non-retryable error returns immediately", func(t *testing.T) {
		boom := errors.New("boom")
		calls := 0
		_, err := dialHopWithRetry(context.Background(), pk,
			func(context.Context, cipher.PubKey) (*Client, error) { calls++; return nil, boom })
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, 1, calls, "must not retry a non-retryable error")
	})

	t.Run("retryable then context canceled in backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, e := dialHopWithRetry(ctx, pk,
				func(context.Context, cipher.PubKey) (*Client, error) {
					return nil, dmsg.ErrCannotConnectToDelegated
				})
			done <- e
		}()
		// Attempt 0 fails (retryable, ctx live) → enters attempt 1's backoff;
		// canceling there returns through the ctx.Done() select branch.
		time.Sleep(100 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			assert.Error(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("dialHopWithRetry did not return after cancellation")
		}
	})
}

func TestDistributionModeString(t *testing.T) {
	cases := map[DistributionMode]string{
		DistributionUnset:           "unset",
		DistributionRoundRobin:      "round-robin",
		DistributionAuto:            "auto",
		DistributionWeighted:        "weighted",
		DistributionSizeThreshold:   "size-threshold",
		DistributionSticky5Tuple:    "sticky:5tuple",
		DistributionLatencyAdaptive: "latency-adaptive",
		DistributionDSCPPriority:    "dscp-priority",
	}
	for m, want := range cases {
		assert.Equalf(t, want, m.String(), "mode %d", int(m))
	}
	assert.Equal(t, "unknown", DistributionMode(99).String())
}
