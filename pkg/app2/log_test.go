package app2

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewLogger tests that after the new logger is created logs with it are persisted into storage
func TestNewLogger(t *testing.T) {
	p, err := ioutil.TempFile("", "test-db")
	require.NoError(t, err)

	defer os.Remove(p.Name()) // nolint

	appName := "foo"

	l, _, err := newPersistentLogger(p.Name(), appName)
	require.NoError(t, err)

	dbl, err := newBoltDB(p.Name(), appName)
	require.NoError(t, err)

	l.Info("bar")

	beginning := time.Unix(0, 0)
	res, err := dbl.(*boltDBappLogs).LogsSince(beginning)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Contains(t, res[0], "bar")
}
