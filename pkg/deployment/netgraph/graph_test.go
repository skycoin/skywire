package netgraph

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildMergesPairs(t *testing.T) {
	g := Build([]Link{
		{A: "a", B: "b", Type: "stcpr"},
		{A: "b", B: "a", Type: "sudph"},
		{A: "a", B: "b", Type: "stcpr"},
		{A: "b", B: "c", Type: "stcpr"},
	}, nil)
	require.Equal(t, []string{"stcpr", "sudph"}, g.Types, "most common type first")
	require.Len(t, g.Edges, 2)
	require.Equal(t, uint8(0b11), g.Edges[0].Mask, "both types join a and b")
	require.Equal(t, []int{3, 4, 1}, g.Degree)
	require.Equal(t, 4, g.Transports)
	for i := range g.Nodes {
		require.False(t, math.IsNaN(g.X[i]) || math.IsNaN(g.Y[i]))
	}
}

func TestWarmStartKeepsThePicture(t *testing.T) {
	var links []Link
	for i := 0; i < 40; i++ {
		links = append(links, Link{A: string(rune('A' + i%20)), B: string(rune('a' + i%13)), Type: "stcpr"})
	}
	g := Build(links, nil)
	h := Build(links, g.Positions())
	moved := 0.0
	for i, k := range h.Nodes {
		p := g.Positions()[k]
		moved += math.Hypot(h.X[i]-p[0], h.Y[i]-p[1])
	}
	require.Less(t, moved/float64(len(h.Nodes)), 0.1, "a refresh must not redraw the graph from scratch")
}

func TestPackEdges(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(packEdges([]Edge{{A: 1, B: 300, Mask: 5}}))
	require.NoError(t, err)
	require.Equal(t, []byte{1, 0, 44, 1, 5}, b)
}

func TestPage(t *testing.T) {
	p := &Page{Title: "T", Source: func(context.Context) ([]Link, error) { return nil, errors.New("cold") }}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/graph", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	p.Source = func(context.Context) ([]Link, error) { return []Link{{A: "a", B: "b", Type: "stcpr"}}, nil }
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/graph", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.Contains(rec.Body.String(), "2 visors, 1 visor pairs, 1 transports"))
}
