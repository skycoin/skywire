// Package router pkg/router/setup_batch_client_test.go
package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	rs "github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

func biRouteTo(src, inter, dst cipher.PubKey, port routing.Port) routing.BidirectionalRoute {
	return routing.BidirectionalRoute{
		Desc: routing.NewRouteDescriptor(src, dst, port, 2),
		Forward: []routing.Hop{
			{TpID: uuid.New(), From: src, To: inter},
			{TpID: uuid.New(), From: inter, To: dst},
		},
		Reverse: []routing.Hop{
			{TpID: uuid.New(), From: dst, To: inter},
			{TpID: uuid.New(), From: inter, To: src},
		},
	}
}

// recordingSender captures the batches the coalescer issued.
type recordingSender struct {
	mu      sync.Mutex
	batches [][]routing.BidirectionalRoute
	node    cipher.PubKey
	err     error
	delay   time.Duration
}

func (s *recordingSender) send(_ context.Context, _ *logging.Logger, _ *dmsg.Client,
	_ []cipher.PubKey, reqs []routing.BidirectionalRoute) ([]batchOutcome, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	s.batches = append(s.batches, append([]routing.BidirectionalRoute(nil), reqs...))
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]batchOutcome, len(reqs))
	for i, r := range reqs {
		out[i] = batchOutcome{rules: routing.EdgeRules{Desc: r.Desc}, node: s.node}
	}
	return out, nil
}

func (s *recordingSender) sizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, len(s.batches))
	for i, b := range s.batches {
		out[i] = len(b)
	}
	return out
}

// Concurrent dials to the SAME exit inside the collection window leave as one
// request. This is the whole coalescer.
func TestSetupBatcher_CollectsSiblingsIntoOneRequest(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.True(t, SetSetupBatchWindow(200*time.Millisecond))

	b := newSetupBatcher()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	node, _ := cipher.GenerateKeyPair()
	sender := &recordingSender{node: node}
	log := logging.MustGetLogger("test")

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	nodes := make([]cipher.PubKey, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inter, _ := cipher.GenerateKeyPair()
			req := biRouteTo(src, inter, dst, routing.Port(100+i)) //nolint:gosec
			_, nodes[i], errs[i] = b.dial(context.Background(), log, nil, nil, req, sender.send)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, node, nodes[i])
	}
	require.Equal(t, []int{n}, sender.sizes(), "six concurrent dials, one request")
	require.EqualValues(t, n, b.stats().Batched)
	require.Zero(t, b.stats().Singles)
}

// setup.batch_max closes a group early instead of letting it grow past the cap.
func TestSetupBatcher_SplitsAtBatchMax(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.True(t, SetSetupBatchWindow(500*time.Millisecond))
	require.True(t, SetSetupBatchMax(3))

	b := newSetupBatcher()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	sender := &recordingSender{}
	log := logging.MustGetLogger("test")

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inter, _ := cipher.GenerateKeyPair()
			_, _, _ = b.dial(context.Background(), log, nil, nil, //nolint:errcheck
				biRouteTo(src, inter, dst, routing.Port(100+i)), sender.send) //nolint:gosec
		}(i)
	}
	wg.Wait()

	total := 0
	for _, s := range sender.sizes() {
		require.LessOrEqual(t, s, 3, "no request exceeds setup.batch_max")
		total += s
	}
	require.Equal(t, 6, total, "every dial was sent exactly once")
}

// Dials to DIFFERENT exits never share a request: the coalescing is only a
// saving because the source and destination are common, and a mixed batch is
// refused by BidirectionalRouteBatch.Check.
func TestSetupBatcher_NeverMixesDestinations(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.True(t, SetSetupBatchWindow(150*time.Millisecond))

	b := newSetupBatcher()
	src, _ := cipher.GenerateKeyPair()
	dstA, _ := cipher.GenerateKeyPair()
	dstB, _ := cipher.GenerateKeyPair()
	sender := &recordingSender{}
	log := logging.MustGetLogger("test")

	var wg sync.WaitGroup
	for i, dst := range []cipher.PubKey{dstA, dstA, dstB, dstB, dstB} {
		wg.Add(1)
		go func(i int, dst cipher.PubKey) {
			defer wg.Done()
			inter, _ := cipher.GenerateKeyPair()
			_, _, _ = b.dial(context.Background(), log, nil, nil, //nolint:errcheck
				biRouteTo(src, inter, dst, routing.Port(100+i)), sender.send) //nolint:gosec
		}(i, dst)
	}
	wg.Wait()

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Len(t, sender.batches, 2, "one request per exit")
	for _, batch := range sender.batches {
		dst := batch[0].Desc.DstPK()
		for _, r := range batch {
			require.Equal(t, dst, r.Desc.DstPK())
		}
	}
}

// An un-upgraded setup node makes the WHOLE group fall back — every member gets
// errBatchUnsupported and runs its own single request, which is today's
// behavior.
func TestSetupBatcher_CapabilityFallback(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.True(t, SetSetupBatchWindow(100*time.Millisecond))

	b := newSetupBatcher()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	sender := &recordingSender{err: errBatchUnsupported}
	log := logging.MustGetLogger("test")

	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inter, _ := cipher.GenerateKeyPair()
			_, _, errs[i] = b.dial(context.Background(), log, nil, nil,
				biRouteTo(src, inter, dst, routing.Port(100+i)), sender.send) //nolint:gosec
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.ErrorIs(t, errs[i], errBatchUnsupported, "every member falls back, none is lost")
	}
	require.EqualValues(t, n, b.stats().Fallback)
}

// setup.batch_max = 1 is the operator's off switch: no dial is ever parked.
func TestSetupBatcher_BatchMaxOneSendsSingles(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.True(t, SetSetupBatchMax(1))

	b := newSetupBatcher()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	inter, _ := cipher.GenerateKeyPair()
	sender := &recordingSender{}

	_, _, err := b.dial(context.Background(), logging.MustGetLogger("test"), nil, nil,
		biRouteTo(src, inter, dst, 100), sender.send)
	require.ErrorIs(t, err, errBatchUnsupported)
	require.Empty(t, sender.sizes(), "nothing was sent in batched form")
}

// A member whose own context expires must not take the group down with it.
func TestSetupBatcher_MemberCancelLeavesTheGroupAlone(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.True(t, SetSetupBatchWindow(80*time.Millisecond))

	b := newSetupBatcher()
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	sender := &recordingSender{delay: 150 * time.Millisecond}
	log := logging.MustGetLogger("test")

	quick, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var quickErr, patientErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		inter, _ := cipher.GenerateKeyPair()
		_, _, quickErr = b.dial(quick, log, nil, nil, biRouteTo(src, inter, dst, 100), sender.send)
	}()
	go func() {
		defer wg.Done()
		inter, _ := cipher.GenerateKeyPair()
		_, _, patientErr = b.dial(context.Background(), log, nil, nil, biRouteTo(src, inter, dst, 101), sender.send)
	}()
	wg.Wait()

	require.True(t, errors.Is(quickErr, context.DeadlineExceeded), "the impatient member gave up")
	require.NoError(t, patientErr, "its sibling's setup completed regardless")
	require.Equal(t, []int{2}, sender.sizes())
}
