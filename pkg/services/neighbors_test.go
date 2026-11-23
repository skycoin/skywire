package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

type neighborsTestReader struct{ found chan Service }

func (r *neighborsTestReader) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func (r *neighborsTestReader) NeighborsSet(n Neighbors) { r.found <- n.NeighborByType("zzz-nb-source") }

type neighborsTestSource struct{}

func (*neighborsTestSource) Run(ctx context.Context) error { <-ctx.Done(); return nil }

// The services of one `svc run` find each other.
func TestRunSetsNeighbors(t *testing.T) {
	reader := &neighborsTestReader{found: make(chan Service, 1)}
	source := &neighborsTestSource{}
	Register("zzz-nb-reader", func(json.RawMessage, *logging.Logger) (Service, error) { return reader, nil })
	Register("zzz-nb-source", func(json.RawMessage, *logging.Logger) (Service, error) { return source, nil })
	f, err := ParseFile([]byte(`{"services":[{"type":"zzz-nb-reader"},{"type":"zzz-nb-source"}]}`))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, f, logging.NewMasterLogger()) }()
	select {
	case got := <-reader.found:
		require.Same(t, source, got)
	case <-time.After(5 * time.Second):
		t.Fatal("the reader was never given its neighbors")
	}
	cancel()
	require.NoError(t, <-done)
}
