package api

import (
	"bytes"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
)

func benchClients(n int) map[cipher.PubKey][]byte {
	clients := make(map[cipher.PubKey][]byte, n)
	for i := 0; i < n; i++ {
		pk, _ := cipher.GenerateKeyPair()
		clients[pk] = []byte(fmt.Sprintf(`{"static":"%s","n":%d}`, pk.Hex(), i))
	}
	return clients
}

// Sorting the raw keys gives the order the hex strings did, so the leaf
// bytes, and its CXO hash, do not change.
func TestEncodeClientsBatchOrderIsHexOrder(t *testing.T) {
	clients := benchClients(500)
	pks := make([]cipher.PubKey, 0, len(clients))
	for pk := range clients {
		pks = append(pks, pk)
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].Hex() < pks[j].Hex() })
	var want bytes.Buffer
	want.WriteByte('[')
	for i, pk := range pks {
		if i > 0 {
			want.WriteByte(',')
		}
		want.Write(clients[pk])
	}
	want.WriteByte(']')
	require.Equal(t, cxoutils.FrameGzip(clientsByServerBatchVersion, want.Bytes()), encodeClientsBatch(clients))
}

func BenchmarkEncodeClientsBatch(b *testing.B) {
	clients := benchClients(1000)
	b.ReportAllocs()
	for b.Loop() {
		encodeClientsBatch(clients)
	}
}
