package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

var ErrNoNodeInMockStore = errors.New("no node with that pk in mock store")

// implements transport-discovery Store, but allows to save plain *transport.Entry in which
// only Edge field is necessary
type mockStore struct {
	transports map[cipher.PubKey][]*transport.Entry
}

func newMockStore() *mockStore {
	return &mockStore{
		transports: make(map[cipher.PubKey][]*transport.Entry),
	}
}

func (m *mockStore) RegisterTransport(context.Context, *transport.SignedEntry) error {
	return nil
}
func (m *mockStore) DeregisterTransport(context.Context, uuid.UUID) error {
	return nil
}
func (m *mockStore) GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error) {
	return nil, nil
}
func (m *mockStore) GetTransportsByEdge(_ context.Context, edgePK cipher.PubKey) ([]*transport.Entry, error) {
	trs, ok := m.transports[edgePK]
	if !ok {
		return nil, ErrNoNodeInMockStore
	}

	return trs, nil
}
func (m *mockStore) GetNumberOfTransports(context.Context) (map[network.Type]int, error) {
	return nil, nil
}
func (m *mockStore) GetAllTransports(context.Context) ([]*transport.Entry, error) {
	return nil, nil
}
func (m *mockStore) Close() {}

// SaveEntry is added to the mock to allow saving Entry, without need for SignedEntry
func (m *mockStore) SaveEntry(source, destiny cipher.PubKey, _ bool) {
	entry := &transport.Entry{
		Edges: transport.SortEdges(source, destiny), // original: [2]cipher.PubKey{source, destiny}
	}
	trs, ok := m.transports[source]
	if !ok {
		trs = make([]*transport.Entry, 0)
	}
	trs = append(trs, entry)
	m.transports[source] = trs

	trd, ok := m.transports[destiny]
	if !ok {
		trd = make([]*transport.Entry, 0)
	}
	trd = append(trd, entry)
	m.transports[destiny] = trd
}

func (m *mockStore) DeleteAll() {
	m.transports = make(map[cipher.PubKey][]*transport.Entry)
}

func TestNewGraph(t *testing.T) {
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

	require.Len(t, g.graph, 5)
	require.Len(t, g.graph[node2PK].connections, 3)

	require.Len(t, g.graph[node2PK].neighbors, 3)
	require.Equal(t, g.graph[node2PK].neighbors[node5PK], g.graph[node3PK].neighbors[node5PK])
}

// In this test node 1 has a loopback, there is no arc between sub-graphs of nodes 1-3 and 4-5
// and nodes 4 and 5 have a bidirectional connection between them. If we start exploring the Graph
// from node 1 or node 4 we should only be able to reach two maximum nodes each time.
func TestNewPartedGraph(t *testing.T) {
	node1PK, _, node3PK, node4PK, node5PK := generateNodesPK(t)

	m := newMockStore()
	m.SaveEntry(node1PK, node1PK, true)
	m.SaveEntry(node1PK, node3PK, true)

	m.SaveEntry(node4PK, node5PK, true)
	m.SaveEntry(node5PK, node4PK, true)

	g, err := NewGraph(context.TODO(), m, node1PK)
	require.NoError(t, err)

	require.Len(t, g.graph, 2)

	g, err = NewGraph(context.TODO(), m, node3PK)
	require.NoError(t, err)

	require.Len(t, g.graph, 2)

	g, err = NewGraph(context.TODO(), m, node4PK)
	require.NoError(t, err)

	require.Len(t, g.graph, 2)
}

func TestAvailability(t *testing.T) {
	node1PK, node2PK, node3PK, node4PK, node5PK := generateNodesPK(t)

	m := newMockStore()
	m.SaveEntry(node1PK, node2PK, true)
	m.SaveEntry(node1PK, node4PK, true)
	m.SaveEntry(node2PK, node3PK, true)
	m.SaveEntry(node2PK, node5PK, true)
	m.SaveEntry(node3PK, node5PK, false)
	m.SaveEntry(node4PK, node5PK, false)

	g, err := NewGraph(context.TODO(), m, node1PK)
	require.NoError(t, err)

	require.Len(t, g.graph, 5)
	require.Len(t, g.graph[node2PK].connections, 3)

	require.Len(t, g.graph[node2PK].neighbors, 3)
}

func TestMarkAndSweep(t *testing.T) {
	node1PK, node2PK, node3PK, node4PK, node5PK := generateNodesPK(t)

	m := newMockStore()
	m.SaveEntry(node1PK, node2PK, true)
	m.SaveEntry(node1PK, node4PK, true)
	m.SaveEntry(node2PK, node3PK, true)
	m.SaveEntry(node3PK, node5PK, true)
	m.SaveEntry(node4PK, node5PK, true)

	g, err := NewGraph(context.TODO(), m, node1PK)
	require.NoError(t, err)
	require.Len(t, g.graph, 5)

	m.DeleteAll()
	m.SaveEntry(node1PK, node1PK, true)
	m.SaveEntry(node1PK, node3PK, true)

	m.SaveEntry(node4PK, node5PK, true)
	m.SaveEntry(node5PK, node4PK, true)

	unreachable, err := g.MarkAndSweep(context.TODO(), node1PK)
	require.NoError(t, err)
	require.Len(t, unreachable, 3)
	require.Len(t, g.graph, 2)
}

func generateNodesPK(t *testing.T) (cipher.PubKey, cipher.PubKey, cipher.PubKey, cipher.PubKey, cipher.PubKey) {
	node1PK, _, err := cipher.GenerateDeterministicKeyPair([]byte(`node1`))
	require.NoError(t, err)
	node2PK, _, err := cipher.GenerateDeterministicKeyPair([]byte(`node2`))
	require.NoError(t, err)
	node3PK, _, err := cipher.GenerateDeterministicKeyPair([]byte(`node3`))
	require.NoError(t, err)
	node4PK, _, err := cipher.GenerateDeterministicKeyPair([]byte(`node4`))
	require.NoError(t, err)
	node5PK, _, err := cipher.GenerateDeterministicKeyPair([]byte(`node5`))
	require.NoError(t, err)
	return node1PK, node2PK, node3PK, node4PK, node5PK
}
