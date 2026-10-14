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
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#DMSGPTYPKS=('')\n#VISORISPUBLIC=false\n"), 0o600))
	conf.Skyenv = envFile

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

// A config that records no skywire.conf was not generated from one, so a live
// change leaves every skywire.conf alone.
func TestNoSkyenvRecordedWritesNothing(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#VISORISPUBLIC=false\n"), 0o600))
	conf.Skyenv = ""
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}

	_, err := v.SetConfigFields(map[string]json.RawMessage{"is_public": json.RawMessage(`true`)})
	require.NoError(t, err)
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, "#VISORISPUBLIC=false\n", string(env))
}

// skyenv names the file the visor writes, so only config gen sets it.
func TestSkyenvIsNotSettable(t *testing.T) {
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	_, err := v.SetConfigFields(map[string]json.RawMessage{"skyenv": json.RawMessage(`"/etc/passwd"`)})
	require.ErrorContains(t, err, "skywire autoconfig")
	require.Empty(t, conf.Skyenv)
}
