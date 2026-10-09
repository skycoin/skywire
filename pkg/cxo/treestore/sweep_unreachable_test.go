package treestore

import (
	"path/filepath"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// Objects that no stored Root reaches are removed when the publisher starts,
// even with a reference count above zero, and the published tree survives.
func TestPublisherStartSweepsUnreachable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	_, sk := cipher.GenerateKeyPair()
	conf := PubConfig{DataDir: dir, BatchWindow: 5 * time.Millisecond}

	pub, err := NewWithTCP("127.0.0.1:0", sk, conf)
	require.NoError(t, err)
	require.NoError(t, pub.Put("a/one", []byte("first")))
	require.NoError(t, pub.Put("b/two", []byte("second")))
	require.NoError(t, pub.Flush())
	require.NoError(t, pub.Close())

	sc := skyobject.NewConfig()
	sc.DataDir = dir
	c, err := skyobject.NewContainer(sc)
	require.NoError(t, err)
	db := c.DB().CXDS()
	before, _ := db.Amount()
	var orphans []skycipher.SHA256
	for i := 0; i < 50; i++ {
		v := []byte{byte(i), 'o', 'r', 'p', 'h', 'a', 'n'}
		k := skycipher.SumSHA256(v)
		_, err := db.Set(k, v, 1)
		require.NoError(t, err)
		orphans = append(orphans, k)
	}
	require.NoError(t, c.Close())

	pub, err = NewWithTCP("127.0.0.1:0", sk, conf)
	require.NoError(t, err)
	v, ok := pub.Get("a/one")
	require.True(t, ok)
	require.Equal(t, "first", string(v))
	v, ok = pub.Get("b/two")
	require.True(t, ok)
	require.Equal(t, "second", string(v))
	require.NoError(t, pub.Close())

	c, err = skyobject.NewContainer(sc)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	for _, k := range orphans {
		_, _, err := c.DB().CXDS().Get(k, 0)
		require.Error(t, err, "orphan %s survived", k.Hex()[:8])
	}
	after, _ := c.DB().CXDS().Amount()
	require.LessOrEqual(t, after, before)
}
