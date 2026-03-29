package dmsgc

import (
	"context"
	"testing"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// mockDiscClient is a minimal disc.APIClient for testing.
type mockDiscClient struct {
	servers []*disc.Entry
	err     error
}

func (m *mockDiscClient) Entry(_ context.Context, _ cipher.PubKey) (*disc.Entry, error) {
	return nil, nil
}
func (m *mockDiscClient) PostEntry(_ context.Context, _ *disc.Entry) error { return nil }
func (m *mockDiscClient) DelEntry(_ context.Context, _ *disc.Entry) error  { return nil }
func (m *mockDiscClient) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	return nil
}
func (m *mockDiscClient) AvailableServers(_ context.Context) ([]*disc.Entry, error) {
	return m.servers, m.err
}
func (m *mockDiscClient) AllServers(_ context.Context) ([]*disc.Entry, error) {
	return m.servers, m.err
}
func (m *mockDiscClient) AllEntries(_ context.Context) ([]string, error) { return nil, nil }
func (m *mockDiscClient) AllClientsByServer(_ context.Context) (map[string][]*disc.Entry, error) {
	return nil, nil
}
func (m *mockDiscClient) ClientsByServer(_ context.Context, _ cipher.PubKey) ([]*disc.Entry, error) {
	return nil, nil
}

func TestLanPriorityDisc(t *testing.T) {
	lanPK, _ := cipher.GenerateKeyPair()
	publicPK, _ := cipher.GenerateKeyPair()

	lanEntry := &disc.Entry{
		Static: lanPK,
		Server: &disc.Server{Address: "192.168.1.1:8085"},
	}
	publicEntry := &disc.Entry{
		Static: publicPK,
		Server: &disc.Server{Address: "1.2.3.4:8080"},
	}

	t.Run("LAN servers appear first in AvailableServers", func(t *testing.T) {
		mock := &mockDiscClient{servers: []*disc.Entry{publicEntry}}
		wrapper := &lanPriorityDisc{APIClient: mock, lanEntries: []*disc.Entry{lanEntry}}

		entries, err := wrapper.AvailableServers(context.Background())
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Equal(t, lanPK, entries[0].Static, "LAN server should be first")
		assert.Equal(t, publicPK, entries[1].Static, "public server should be second")
	})

	t.Run("LAN servers appear first in AllServers", func(t *testing.T) {
		mock := &mockDiscClient{servers: []*disc.Entry{publicEntry}}
		wrapper := &lanPriorityDisc{APIClient: mock, lanEntries: []*disc.Entry{lanEntry}}

		entries, err := wrapper.AllServers(context.Background())
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Equal(t, lanPK, entries[0].Static)
	})

	t.Run("LAN servers returned even when public discovery fails", func(t *testing.T) {
		mock := &mockDiscClient{err: assert.AnError}
		wrapper := &lanPriorityDisc{APIClient: mock, lanEntries: []*disc.Entry{lanEntry}}

		entries, err := wrapper.AvailableServers(context.Background())
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, lanPK, entries[0].Static)
	})

	t.Run("error propagated when no LAN servers and public fails", func(t *testing.T) {
		mock := &mockDiscClient{err: assert.AnError}
		wrapper := &lanPriorityDisc{APIClient: mock, lanEntries: nil}

		_, err := wrapper.AvailableServers(context.Background())
		assert.Error(t, err)
	})

	t.Run("other methods delegate to underlying client", func(t *testing.T) {
		mock := &mockDiscClient{}
		wrapper := &lanPriorityDisc{APIClient: mock, lanEntries: []*disc.Entry{lanEntry}}

		// Entry, PostEntry, etc. should pass through
		_, err := wrapper.Entry(context.Background(), lanPK)
		assert.NoError(t, err)
	})
}
