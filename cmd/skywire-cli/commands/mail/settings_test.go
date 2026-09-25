package climail

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"16MiB": 16 << 20, "1mib": 1 << 20, "500KB": 500000, "2GiB": 2 << 30,
		"1048576": 1 << 20, "1.5MiB": 3 << 19, "default": 0, "none": -1, "unlimited": -1,
	} {
		got, err := parseSize(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "MiB", "-5MiB", "0", "lots"} {
		_, err := parseSize(bad)
		require.Error(t, err, bad)
	}
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "0.5d": 12 * time.Hour,
		"default": 0, "none": -1, "forever": -1,
	} {
		got, err := parseAge(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "d", "-1d", "0s", "soon"} {
		_, err := parseAge(bad)
		require.Error(t, err, bad)
	}
}

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

func TestFormat(t *testing.T) {
	require.Equal(t, "16MiB", formatSize(16<<20))
	require.Equal(t, "1.5MiB", formatSize(3<<19))
	require.Equal(t, "2.0KiB", formatSize(2048))
	require.Equal(t, "no limit", formatSize(-1))
	require.Equal(t, "7d", formatAge(7*24*time.Hour))
	require.Equal(t, "never", formatAge(-1))
}

func TestSafeNameStaysInTheDirectory(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf": "report.pdf", "../../etc/passwd": "passwd", `..\..\win.ini`: "win.ini",
		"/abs/path/x": "x", "": "attachment", "..": "attachment", ".": "attachment",
	} {
		require.Equal(t, want, safeName(in), in)
	}
}

func TestReadAttachmentsBase64(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})
	att, err := readAttachments(nil, []string{"a.png=" + enc})
	require.NoError(t, err)
	require.Equal(t, "a.png", att[0].Name)
	require.Equal(t, "image/png", att[0].ContentType)
	require.Equal(t, []byte{0, 1, 2, 255}, att[0].Data)
	_, err = readAttachments(nil, []string{"nobase64"})
	require.Error(t, err)
	_, err = readAttachments(nil, []string{"x=!!!"})
	require.Error(t, err)
}
