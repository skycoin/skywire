package visor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestLocalRelaySocketPath(t *testing.T) {
	require.Equal(t, filepath.Join("/var/lib/skywire", "dmsg_relay.sock"),
		localRelaySocketPath(&spec.DmsgLocalRelayConfig{}, "/var/lib/skywire"))
	require.Equal(t, "/run/skywire/relay.sock",
		localRelaySocketPath(&spec.DmsgLocalRelayConfig{Socket: "/run/skywire/relay.sock"}, "/var/lib/skywire"))
	// "-" opts out of the unix socket entirely (TCPAddress-only deployments).
	require.Equal(t, "", localRelaySocketPath(&spec.DmsgLocalRelayConfig{Socket: "-"}, "/var/lib/skywire"))
}

func TestParseSocketMode(t *testing.T) {
	m, err := parseSocketMode("")
	require.NoError(t, err)
	require.Equal(t, defaultLocalRelaySocketMode, m)

	m, err = parseSocketMode("0660")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0660), m)

	// A decimal-looking "600" is still read as octal, which is what an
	// operator writing a file mode means. A non-octal digit is an error
	// rather than a silent 0.
	_, err = parseSocketMode("0o600")
	require.Error(t, err)
	_, err = parseSocketMode("rw-------")
	require.Error(t, err)
}

// TestListenLocalRelaySocketMode is the security-relevant one: the socket must
// end up owner-only, because with no allowed_keys its file mode is the ONLY
// gate on a grant of the visor's transports.
func TestListenLocalRelaySocketMode(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "sub", "relay.sock")
	lis, err := listenLocalRelaySocket(sock, "", logging.MustGetLogger("test"))
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	fi, err := os.Stat(sock)
	require.NoError(t, err)
	require.Equal(t, defaultLocalRelaySocketMode, fi.Mode().Perm())

	// The parent directory is the belt to that braces: net.Listen creates the
	// socket with 0777&^umask and only then can it be chmodded.
	di, err := os.Stat(filepath.Dir(sock))
	require.NoError(t, err)
	require.Equal(t, localRelaySocketDirMode, di.Mode().Perm())
}

// TestListenLocalRelaySocketReplacesStale covers the restart path: a visor
// killed without running its close stack leaves the socket file behind, and
// bind() on an existing path fails whether or not anything is listening — so
// the second start must unlink before it binds.
func TestListenLocalRelaySocketReplacesStale(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	require.NoError(t, os.WriteFile(sock, nil, 0600))

	lis, err := listenLocalRelaySocket(sock, "", logging.MustGetLogger("test"))
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	fi, err := os.Stat(sock)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSocket, "stale file should have been replaced by a socket")
	require.Equal(t, defaultLocalRelaySocketMode, fi.Mode().Perm())
}

func TestCheckLocalRelayTCP(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	// No key list: refused outright. A TCP listener carries no evidence about
	// who opened it, so without allowed_keys there is no gate at all.
	require.Error(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: "127.0.0.1:7070"}))

	ok := &spec.DmsgLocalRelayConfig{TCPAddress: "127.0.0.1:7070", AllowedKeys: []cipher.PubKey{pk}}
	require.NoError(t, checkLocalRelayTCP(ok))
	require.NoError(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: "localhost:7070", AllowedKeys: []cipher.PubKey{pk}}))
	require.NoError(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: "[::1]:7070", AllowedKeys: []cipher.PubKey{pk}}))

	// Anything reachable from off-host is refused even WITH a key list: an
	// allowlist is not a DoS defense, and this feature must not become a
	// network-exposed path to the visor's transports.
	require.Error(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: "0.0.0.0:7070", AllowedKeys: []cipher.PubKey{pk}}))
	require.Error(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: "192.168.1.5:7070", AllowedKeys: []cipher.PubKey{pk}}))
	require.Error(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: ":7070", AllowedKeys: []cipher.PubKey{pk}}))
	require.Error(t, checkLocalRelayTCP(&spec.DmsgLocalRelayConfig{TCPAddress: "no-port", AllowedKeys: []cipher.PubKey{pk}}))
}

func TestLocalRelayAllow(t *testing.T) {
	selfPK, _ := cipher.GenerateKeyPair()
	listed, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	// Empty list: the listener is the gate, so any proven key is admitted —
	// except the visor's own, which two clients must never share.
	open, err := localRelayAllow(&spec.DmsgLocalRelayConfig{}, selfPK)
	require.NoError(t, err)
	require.True(t, open(listed))
	require.True(t, open(other))
	require.False(t, open(selfPK))
	require.False(t, open(cipher.PubKey{}))

	// With a list it is an allowlist, and the Noise handshake is what makes
	// membership meaningful.
	closed, err := localRelayAllow(&spec.DmsgLocalRelayConfig{AllowedKeys: []cipher.PubKey{listed}}, selfPK)
	require.NoError(t, err)
	require.True(t, closed(listed))
	require.False(t, closed(other))
	require.False(t, closed(selfPK))

	// Config errors are refused at build time, not silently tolerated.
	_, err = localRelayAllow(&spec.DmsgLocalRelayConfig{AllowedKeys: []cipher.PubKey{{}}}, selfPK)
	require.Error(t, err)
	_, err = localRelayAllow(&spec.DmsgLocalRelayConfig{AllowedKeys: []cipher.PubKey{selfPK}}, selfPK)
	require.Error(t, err)
}
