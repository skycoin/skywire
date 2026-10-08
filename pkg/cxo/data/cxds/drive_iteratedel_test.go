package cxds

import (
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

func fillDrive(t testing.TB, n int) (*driveCXDS, []cipher.SHA256) {
	t.Helper()
	ds, err := NewDriveCXDS(filepath.Join(t.TempDir(), "cxds.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ds.Close() }) //nolint:errcheck
	d := ds.(*driveCXDS)
	keys := make([]cipher.SHA256, 0, n)
	require.NoError(t, d.RunBatch(func(s data.CXDS) error {
		for i := 0; i < n; i++ {
			v := make([]byte, 16)
			binary.BigEndian.PutUint64(v, uint64(i))
			k := cipher.SumSHA256(v)
			if _, err := s.Set(k, v, 1); err != nil {
				return err
			}
			if i%3 == 0 {
				if _, err := s.Inc(k, -1); err != nil {
					return err
				}
			}
			keys = append(keys, k)
		}
		return nil
	}))
	return d, keys
}

// IterateDel visits every object once and deletes exactly the ones asked for,
// including runs of neighbors and the first and last keys.
func TestDriveIterateDelVisitsAllAndDeletes(t *testing.T) {
	d, keys := fillDrive(t, 3000)
	seen := map[cipher.SHA256]bool{}
	var zero int
	require.NoError(t, d.IterateDel(func(k cipher.SHA256, rc uint32, _ []byte) (bool, error) {
		require.False(t, seen[k], "visited twice")
		seen[k] = true
		if rc == 0 {
			zero++
			return true, nil
		}
		return false, nil
	}))
	require.Len(t, seen, len(keys))
	left := 0
	require.NoError(t, d.Iterate(func(_ cipher.SHA256, rc uint32, _ []byte) error {
		require.NotZero(t, rc)
		left++
		return nil
	}))
	require.Equal(t, len(keys)-zero, left)
	all, _ := d.Amount()
	require.Equal(t, left, all)
}

func BenchmarkDriveIterateDelNoop(b *testing.B) {
	d, _ := fillDrive(b, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.IterateDel(func(cipher.SHA256, uint32, []byte) (bool, error) { return false, nil }) //nolint:errcheck
	}
}
