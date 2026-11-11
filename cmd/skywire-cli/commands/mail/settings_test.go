package climail

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
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

func TestParseHelperLists(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	u, err := parseSettings([]string{"helpers=" + pk.Hex(), "relay_for=none"})
	require.NoError(t, err)
	require.Equal(t, []cipher.PubKey{pk}, *u.Helpers)
	require.NotNil(t, u.RelayFor)
	require.Empty(t, *u.RelayFor)
	u, err = parseSettings([]string{"helpers=default", "relay_for=default"})
	require.NoError(t, err)
	require.Nil(t, u.Helpers)
	require.True(t, u.HelpersDefault)
	require.True(t, u.RelayForDefault)
	_, err = parseSettings([]string{"helpers=nope"})
	require.Error(t, err)

	out := renderSettings(&visorapi.MailStatus{HasOutbox: true, Helpers: []cipher.PubKey{pk}, RelayForDefault: true})
	require.Contains(t, out, "helpers           "+pk.Hex()+"\n")
	require.Contains(t, out, "relay_for         none (default: this visor's hypervisors)")
}
