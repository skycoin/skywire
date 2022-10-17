package transport_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

func testTransportLogStore(t *testing.T, logStore transport.LogStore) {
	t.Helper()

	id1 := uuid.New()

	entry1 := transport.NewLogEntry()
	entry1.AddRecv(100)
	entry1.AddSent(200)

	id2 := uuid.New()
	entry2 := transport.NewLogEntry()
	entry2.AddRecv(300)
	entry2.AddSent(400)

	require.NoError(t, logStore.Record(id1, entry1))
	require.NoError(t, logStore.Record(id2, entry2))

	entry, err := logStore.Entry(id2)
	require.NoError(t, err)
	assert.Equal(t, uint64(300), *entry.RecvBytes)
	assert.Equal(t, uint64(400), *entry.SentBytes)
}

func TestInMemoryTransportLogStore(t *testing.T) {
	testTransportLogStore(t, transport.InMemoryTransportLogStore())
}

func TestFileTransportLogStore(t *testing.T) {
	dir, err := os.MkdirTemp("", "log_store")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, os.RemoveAll(dir))
	}()

	log := logging.MustGetLogger("transport")
	ls, err := transport.FileTransportLogStore(context.TODO(), dir, time.Hour*24*7, log)
	require.NoError(t, err)
	testTransportLogStore(t, ls)
}

func TestLogEntry_MarshalJSON(t *testing.T) {
	entry := transport.NewLogEntry()
	entry.AddSent(10)
	entry.AddRecv(100)
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	fmt.Println(string(b))
	b, err = json.MarshalIndent(entry, "", "\t")
	require.NoError(t, err)
	fmt.Println(string(b))
}

func TestLogEntry_GobEncode(t *testing.T) {
	entry := transport.NewLogEntry()

	enc, err := entry.GobEncode()
	require.NoError(t, err)

	require.NoError(t, entry.GobDecode(enc))
}
