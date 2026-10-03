package visor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
)

// Every mirrored path must name a real config field and an autoconfig flag
// that writes skywire.conf, or the mirror silently drops it.
func TestAutoconfigFlagForIsValid(t *testing.T) {
	conf, _ := newTestConfig(t)
	root := reflect.ValueOf(conf).Elem()
	for path, flagFor := range autoconfigFlagFor {
		tgt, err := resolveConfigTarget(root, path)
		require.NoError(t, err, path)
		flag, val := flagFor(reflect.Zero(tgt.typ))
		_, err = autoconfigcmd.EditFor(flag, val)
		require.NoError(t, err, path)
	}
}

func TestSetPtyWhitelistLiveAndMirrored(t *testing.T) {
	conf, path := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#DMSGPTYPKS=('')\n#VISORISPUBLIC=false\n"), 0o600))
	restore := skyenvPaths
	skyenvPaths = func() (string, string) { return path, envFile }
	t.Cleanup(func() { skyenvPaths = restore })

	hv, _ := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	conf.Hypervisors = []cipher.PubKey{hv}
	conf.Pty.Whitelist = []cipher.PubKey{hv, a}
	wl := pty.NewMemoryWhitelist()
	require.NoError(t, wl.Add(conf.PK, hv, a))
	v := &Visor{conf: conf, peerWhitelist: wl, log: logging.MustGetLogger("test")}

	changes, err := v.SetConfigFields(map[string]json.RawMessage{
		"pty.whitelist": json.RawMessage(`["` + b.String() + `"]`),
		"is_public":     json.RawMessage(`true`),
	})
	require.NoError(t, err)
	require.True(t, changes[1].Live, changes[1].Path)

	ok, _ := wl.Get(b) //nolint:errcheck
	require.True(t, ok, "an added key is admitted at once")
	ok, _ = wl.Get(a) //nolint:errcheck
	require.False(t, ok, "a removed key loses trust")
	ok, _ = wl.Get(hv) //nolint:errcheck
	require.True(t, ok, "a hypervisor keeps its trust")
	require.Equal(t, []cipher.PubKey{b}, conf.Pty.Whitelist)

	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "DMSGPTYPKS=('"+b.String()+"')")
	require.True(t, strings.Contains(string(env), "VISORISPUBLIC=true"), string(env))
}
