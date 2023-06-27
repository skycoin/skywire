package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDijkstra(t *testing.T) {
	node1PK, node2PK, node3PK, node4PK, node5PK := generateNodesPK(t)

	m := newMockStore()
	m.SaveEntry(node1PK, node2PK, true)
	m.SaveEntry(node1PK, node4PK, true)
	m.SaveEntry(node2PK, node3PK, true)
	m.SaveEntry(node2PK, node5PK, true)
	m.SaveEntry(node3PK, node5PK, true)
	m.SaveEntry(node4PK, node5PK, true)

	g, err := NewGraph(context.TODO(), m, node1PK)
	require.NoError(t, err)

	routes, err := g.Shortest(context.TODO(), node1PK, node5PK, 0, 100, 5)
	require.NoError(t, err)
	require.Len(t, routes, 3)

	routes, err = g.Shortest(context.TODO(), node1PK, node5PK, 0, 2, 5)
	require.NoError(t, err)
	require.Len(t, routes, 2)

	routes, err = g.Shortest(context.TODO(), node1PK, node5PK, 0, 100, 1)
	require.NoError(t, err)
	require.Len(t, routes, 1)
}

func TestDijkstraRoute(t *testing.T) {
	node1PK, node2PK, node3PK, node4PK, node5PK := generateNodesPK(t)

	m := newMockStore()
	m.SaveEntry(node1PK, node2PK, true)
	m.SaveEntry(node1PK, node4PK, true)
	m.SaveEntry(node2PK, node3PK, true)
	m.SaveEntry(node2PK, node5PK, true)
	m.SaveEntry(node3PK, node5PK, true)

	g, err := NewGraph(context.TODO(), m, node1PK)
	require.NoError(t, err)

	routes, err := g.Shortest(context.TODO(), node1PK, node5PK, 0, 100, 1)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, routes[0].Hops[0].From, node1PK)
	require.Equal(t, routes[0].Hops[0].To, node2PK)
	require.Equal(t, routes[0].Hops[1].From, node2PK)
	require.Equal(t, routes[0].Hops[1].To, node5PK)
}

func TestNoRoutesFromNoPathWithRootNodes(t *testing.T) {
	node1PK, node2PK, node3PK, node4PK, _ := generateNodesPK(t)

	m := newMockStore()
	m.SaveEntry(node1PK, node2PK, true)
	m.SaveEntry(node1PK, node3PK, true)
	m.SaveEntry(node2PK, node3PK, false)
	m.SaveEntry(node2PK, node4PK, false)
	m.SaveEntry(node3PK, node4PK, true)

	g, err := NewGraph(context.TODO(), m, node1PK)
	require.NoError(t, err)

	require.Len(t, g.graph[node4PK].neighbors, 2)
	require.Equal(t, g.graph[node4PK].neighbors[node3PK].edge, node3PK)
	c, f := context.WithTimeout(context.Background(), 2*time.Second)
	defer f()

	r, err := g.Shortest(c, node1PK, node3PK, 0, 100, 5)
	require.NoError(t, err)
	fmt.Println(r)
}
