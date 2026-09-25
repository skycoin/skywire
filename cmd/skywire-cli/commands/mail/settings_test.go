package climail

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSettings(t *testing.T) {
	u, err := parseSettings([]string{"enable=false", "max_total_size=64MiB", "max_age=30d", "max_message_size=none"})
	require.NoError(t, err)
	require.False(t, *u.Enable)
	require.Equal(t, int64(64<<20), *u.MaxTotalSize)
	require.Equal(t, 30*24*time.Hour, *u.MaxAge)
	require.Equal(t, int64(-1), *u.MaxMessageSize)
	_, err = parseSettings([]string{"flavor=blue"})
	require.Error(t, err)
	_, err = parseSettings([]string{"enable"})
	require.Error(t, err)
}

func TestSafeNameStaysInTheDirectory(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf": "report.pdf", "../../etc/passwd": "passwd", `..\..\win.ini`: "win.ini",
		"/abs/path/x": "x", "": "attachment", "..": "attachment", ".": "attachment",
	} {
		require.Equal(t, want, safeName(in), in)
	}
}

func TestReadAttachmentsFromFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.png")
	require.NoError(t, os.WriteFile(p, []byte{0, 1, 2, 255}, 0o600))
	att, err := readAttachments([]string{p})
	require.NoError(t, err)
	require.Equal(t, "a.png", att[0].Name)
	require.Equal(t, "image/png", att[0].ContentType)
	require.Equal(t, []byte{0, 1, 2, 255}, att[0].Data)
	_, err = readAttachments([]string{p + ".missing"})
	require.Error(t, err)
}
