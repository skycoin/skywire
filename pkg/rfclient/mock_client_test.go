package rfclient

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// These tests exercise the generated MockClient's return-value dispatch so
// that future users of the mock (and the mock itself) stay covered.

func TestMockClient_ValueReturn(t *testing.T) {
	m := NewMockClient(t)
	edges := testEdges(t)
	want := map[routing.PathEdges][][]routing.Hop{
		edges: {{{TpID: uuid.New(), From: edges[0], To: edges[1]}}},
	}
	m.On("FindRoutes", mock.Anything, mock.Anything, mock.Anything).Return(want, nil)

	got, err := m.FindRoutes(context.Background(), []routing.PathEdges{edges}, nil)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestMockClient_NilValueAndError(t *testing.T) {
	m := NewMockClient(t)
	m.On("FindRoutes", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("boom"))

	got, err := m.FindRoutes(context.Background(), nil, nil)
	require.Nil(t, got)
	require.EqualError(t, err, "boom")
}

func TestMockClient_CombinedFuncReturn(t *testing.T) {
	m := NewMockClient(t)
	edges := testEdges(t)
	want := map[routing.PathEdges][][]routing.Hop{edges: {{}}}

	// A single func returning (map, error) hits the combined-func branch.
	m.On("FindRoutes", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ []routing.PathEdges, _ *RouteOptions) (map[routing.PathEdges][][]routing.Hop, error) {
			return want, nil
		},
	)

	got, err := m.FindRoutes(context.Background(), []routing.PathEdges{edges}, &RouteOptions{})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestMockClient_SeparateFuncReturns(t *testing.T) {
	m := NewMockClient(t)
	edges := testEdges(t)
	want := map[routing.PathEdges][][]routing.Hop{edges: {{}}}

	// Separate funcs hit the r0-func and r1-func branches independently.
	m.On("FindRoutes", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ []routing.PathEdges, _ *RouteOptions) map[routing.PathEdges][][]routing.Hop {
			return want
		},
		func(_ context.Context, _ []routing.PathEdges, _ *RouteOptions) error {
			return errors.New("func-err")
		},
	)

	got, err := m.FindRoutes(context.Background(), []routing.PathEdges{edges}, nil)
	require.Equal(t, want, got)
	require.EqualError(t, err, "func-err")
}

func TestMockClient_NoReturnPanics(t *testing.T) {
	// A bare mock (not NewMockClient → no AssertExpectations cleanup) with an
	// expectation that specifies no return values triggers the guard panic.
	m := &MockClient{}
	m.On("FindRoutes", mock.Anything, mock.Anything, mock.Anything).Return()
	require.PanicsWithValue(t, "no return value specified for FindRoutes", func() {
		_, _ = m.FindRoutes(context.Background(), nil, nil)
	})
}
