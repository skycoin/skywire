package cliconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The generated config records the skywire.conf it came from, as an absolute
// path, and nothing when there was none.
func TestSkyenvSource(t *testing.T) {
	restore := skyenvfile
	t.Cleanup(func() { skyenvfile = restore })

	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("skywire.conf", nil, 0o600))

	skyenvfile = "skywire.conf"
	require.Equal(t, filepath.Join(dir, "skywire.conf"), skyenvSource())
	skyenvfile = "missing.conf"
	require.Empty(t, skyenvSource())
	skyenvfile = ""
	require.Empty(t, skyenvSource())
}
