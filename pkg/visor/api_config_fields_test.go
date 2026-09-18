package visor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	appspec "github.com/skycoin/skywire/pkg/app/appserver/spec"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	tnspec "github.com/skycoin/skywire/pkg/transport/network/spec"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// newTestConfig builds a file-backed V1 with the blocks the path tests address,
// including the persisted maps other subsystems own (they must survive a flush).
func newTestConfig(t *testing.T) (*visorconfig.V1, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "skywire-config.json")

	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, path, &sk)
	require.NoError(t, err)

	conf := &visorconfig.V1{
		Common:   common,
		LogLevel: "info",
		Transport: &visorconfig.Transport{
			TransportPort: 0,
			StcprPort:     1,
			LogStore:      &visorconfig.LogStore{Type: "file"},
		},
		Routing: &visorconfig.Routing{
			MinHops:           0,
			RouterSettings:    map[string]string{"ecf_max_window_bytes": "4MiB"},
			RouterAppSettings: map[string]map[string]string{"skysocks-client": {"dead_route_hold": "5s"}},
		},
		Launcher: &visorconfig.Launcher{
			Apps: []appspec.AppConfig{
				{Name: "skysocks", AutoStart: true, Port: routing.Port(3)},
				{Name: "vpn-client", AutoStart: false, Port: routing.Port(43)},
			},
		},
		STCP:        &tnspec.STCPConfig{},
		AppSettings: map[string]visorconfig.AppSettingsEntry{"skysocks-client": {}},
	}
	initial, err := json.MarshalIndent(conf, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, initial, 0o600))
	return conf, path
}

func TestResolveConfigTarget(t *testing.T) {
	conf, _ := newTestConfig(t)
	root := reflect.ValueOf(conf).Elem()

	t.Run("nested field", func(t *testing.T) {
		tgt, err := resolveConfigTarget(root, "transport.transport_port")
		require.NoError(t, err)
		require.Equal(t, "transport.transport_port", tgt.tmpl)
		require.Equal(t, reflect.Int, tgt.typ.Kind())
		require.EqualValues(t, 0, tgt.get().Int())
	})

	t.Run("top-level field", func(t *testing.T) {
		tgt, err := resolveConfigTarget(root, "is_public")
		require.NoError(t, err)
		require.Equal(t, "is_public", tgt.tmpl)
	})

	t.Run("embedded common field", func(t *testing.T) {
		// version lives on the embedded *Common; the walk must descend into it.
		tgt, err := resolveConfigTarget(root, "version")
		require.NoError(t, err)
		require.Equal(t, reflect.String, tgt.typ.Kind())
	})

	t.Run("apps by name", func(t *testing.T) {
		tgt, err := resolveConfigTarget(root, "launcher.apps[vpn-client].auto_start")
		require.NoError(t, err)
		require.Equal(t, "launcher.apps[*].auto_start", tgt.tmpl)
		require.Equal(t, "vpn-client", tgt.idx)
		require.False(t, tgt.get().Bool())
		// Writing through the target must hit the SECOND entry, not the first.
		tgt.set(reflect.ValueOf(true))
		require.True(t, conf.Launcher.Apps[1].AutoStart)
		require.True(t, conf.Launcher.Apps[0].AutoStart, "the skysocks entry must be untouched")
	})

	t.Run("map index", func(t *testing.T) {
		tgt, err := resolveConfigTarget(root, "routing.router_settings[ecf_max_window_bytes]")
		require.NoError(t, err)
		require.Equal(t, "routing.router_settings[*]", tgt.tmpl)
		require.Equal(t, "ecf_max_window_bytes", tgt.idx)
		require.Equal(t, "4MiB", tgt.get().String())
	})

	t.Run("absent map key reads null", func(t *testing.T) {
		tgt, err := resolveConfigTarget(root, "routing.router_settings[not_set_yet]")
		require.NoError(t, err)
		raw, err := marshalConfigValue(tgt.get())
		require.NoError(t, err)
		require.Equal(t, "null", string(raw))
	})

	t.Run("errors", func(t *testing.T) {
		for _, tc := range []struct{ path, want string }{
			{"transport.no_such_field", "unknown config path"},
			{"no_such_block.x", "unknown config path"},
			{"launcher.apps[nope].auto_start", `no entry named "nope"`},
			{"transport.transport_port.deeper", "is not a config block"},
			{"routing.router_settings[a].b", "must be the last path element"},
			{"launcher.apps[].auto_start", "empty name or index"},
			{"launcher.apps[x.auto_start", "unterminated"},
			{"", "empty config path"},
			{"transport..stcpr_port", "empty path element"},
		} {
			_, err := resolveConfigTarget(root, tc.path)
			require.Error(t, err, tc.path)
			require.Contains(t, err.Error(), tc.want, tc.path)
		}
	})
}

// TestResolveConfigTargetRefusesSecrets pins the identity/secret refusal: no
// path may reach the visor secret key, the public key, or any embedded *_sk.
func TestResolveConfigTargetRefusesSecrets(t *testing.T) {
	conf, _ := newTestConfig(t)
	root := reflect.ValueOf(conf).Elem()
	for _, path := range []string{"sk", "pk", "routing.route_setup_sk", "transport.tps_sk", "survey_client_sk"} {
		_, err := resolveConfigTarget(root, path)
		require.Error(t, err, path)
		require.Contains(t, err.Error(), "not editable", path)
	}
}

// TestLiveConfigFieldsResolve is the regression that keeps the live table honest:
// every registered path must still exist on visorconfig.V1. A renamed config
// field fails here instead of silently downgrading a live knob to
// restart-required at runtime.
func TestLiveConfigFieldsResolve(t *testing.T) {
	conf, _ := newTestConfig(t)
	root := reflect.ValueOf(conf).Elem()
	for _, f := range liveConfigFieldTable {
		concrete := strings.ReplaceAll(f.Path, "apps[*]", "apps[skysocks]")
		concrete = strings.ReplaceAll(concrete, "router_settings[*]", "router_settings[ecf_max_window_bytes]")
		tgt, err := resolveConfigTarget(root, concrete)
		require.NoError(t, err, f.Path)
		require.Equal(t, f.Path, tgt.tmpl, "template mismatch for %s", f.Path)
		require.NotEmpty(t, f.Desc, "%s needs a description for the command help", f.Path)
	}
	require.NotEmpty(t, LiveConfigFields())
}

func TestDecodeConfigValue(t *testing.T) {
	strType := reflect.TypeOf("")
	intType := reflect.TypeOf(int(0))
	boolType := reflect.TypeOf(false)

	// A bare word is accepted for a string field (what the CLI sends after
	// quoting), as is the already-quoted form.
	for _, raw := range []string{`"debug"`, `debug`} {
		v, err := decodeConfigValue(strType, json.RawMessage(raw))
		require.NoError(t, err, raw)
		require.Equal(t, "debug", v.String())
	}
	// A quoted number still lands in an int field.
	for _, raw := range []string{`7777`, `"7777"`} {
		v, err := decodeConfigValue(intType, json.RawMessage(raw))
		require.NoError(t, err, raw)
		require.EqualValues(t, 7777, v.Int())
	}
	v, err := decodeConfigValue(boolType, json.RawMessage(`true`))
	require.NoError(t, err)
	require.True(t, v.Bool())

	// Type mismatches are refused.
	_, err = decodeConfigValue(intType, json.RawMessage(`"not-a-number"`))
	require.Error(t, err)
	_, err = decodeConfigValue(boolType, json.RawMessage(`"maybe"`))
	require.Error(t, err)
}

// TestSetConfigFieldsRestartRequired covers the whole restart-required path:
// classification, the printed line, the on-disk write, and — the property the
// other subsystems depend on — that the flush keeps the maps they own.
func TestSetConfigFieldsRestartRequired(t *testing.T) {
	conf, path := newTestConfig(t)
	v := &Visor{conf: conf}

	changes, err := v.SetConfigFields(map[string]json.RawMessage{
		"transport.transport_port":       json.RawMessage(`7777`),
		"launcher.apps[vpn-client].port": json.RawMessage(`4343`),
		"transport.no_direct_transports": json.RawMessage(`true`),
	})
	require.NoError(t, err)
	require.Len(t, changes, 3)

	// Sorted by path, each classified restart-required.
	require.Equal(t, "launcher.apps[vpn-client].port", changes[0].Path)
	for _, c := range changes {
		require.False(t, c.Live, c.Path)
		require.Contains(t, c.String(), "(restart-required)")
	}
	require.Equal(t, "0", string(changes[2].Old))
	require.Equal(t, "7777", string(changes[2].New))

	require.Equal(t, 7777, conf.Transport.TransportPort)
	require.EqualValues(t, 4343, conf.Launcher.Apps[1].Port)
	require.EqualValues(t, 3, conf.Launcher.Apps[0].Port, "the other app entry must be untouched")

	// One flush for the batch, and the persisted maps survive it.
	onDisk, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(onDisk, &got))

	tp, _ := got["transport"].(map[string]interface{})
	require.EqualValues(t, 7777, tp["transport_port"])

	rt, _ := got["routing"].(map[string]interface{})
	rs, _ := rt["router_settings"].(map[string]interface{})
	require.Equal(t, "4MiB", rs["ecf_max_window_bytes"], "router_settings must survive the flush")
	ras, _ := rt["router_app_settings"].(map[string]interface{})
	require.Contains(t, ras, "skysocks-client", "router_app_settings must survive the flush")
	require.Contains(t, got, "app_settings", "app_settings must survive the flush")
	require.Contains(t, got, "sk", "the visor's own flush still writes the real sk")
}

// TestSetConfigFieldsAtomic: validation runs over every field before any is
// applied, so a bad path leaves the good ones alone.
func TestSetConfigFieldsAtomic(t *testing.T) {
	conf, path := newTestConfig(t)
	before, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	v := &Visor{conf: conf}

	_, err = v.SetConfigFields(map[string]json.RawMessage{
		"transport.transport_port": json.RawMessage(`7777`),
		"transport.nonexistent":    json.RawMessage(`1`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown config path")
	require.Equal(t, 0, conf.Transport.TransportPort, "nothing may be applied when validation fails")

	after, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "no flush on a failed validation")

	// A type mismatch is caught the same way.
	_, err = v.SetConfigFields(map[string]json.RawMessage{
		"transport.transport_port": json.RawMessage(`"not-a-port"`),
	})
	require.Error(t, err)
	require.Equal(t, 0, conf.Transport.TransportPort)
}

// TestSetConfigFieldsRefusals: identity, unknown paths, empty calls, and the
// maps another subsystem rewrites wholesale.
func TestSetConfigFieldsRefusals(t *testing.T) {
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf}

	_, sk := cipher.GenerateKeyPair()
	for path, want := range map[string]string{
		"sk":                                   "not editable",
		"pk":                                   "not editable",
		"routing.route_setup_sk":               "not editable",
		"app_settings":                         "proxy settings",
		"routing.router_app_settings":          "route settings",
		"routing.router_settings":              "route settings",
		"launcher.apps[not-an-app].auto_start": "no entry named",
	} {
		_, err := v.SetConfigFields(map[string]json.RawMessage{path: json.RawMessage(`"` + sk.Hex() + `"`)})
		require.Error(t, err, path)
		require.Contains(t, err.Error(), want, path)
	}

	_, err := v.SetConfigFields(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no fields given")

	// A per-knob router_settings write is NOT refused — it is a live path.
	tgt, err := resolveConfigTarget(reflect.ValueOf(conf).Elem(), "routing.router_settings[dead_route_hold]")
	require.NoError(t, err)
	_, isLive := liveConfigFieldByPath[tgt.tmpl]
	require.True(t, isLive)
}

// TestSetConfigFieldsNotFileBacked: a restart-required field needs somewhere to
// be written down; an in-memory config refuses rather than silently dropping it.
func TestSetConfigFieldsNotFileBacked(t *testing.T) {
	common, err := visorconfig.NewCommon(nil, "", nil)
	require.NoError(t, err)
	conf := &visorconfig.V1{Common: common, Transport: &visorconfig.Transport{}}
	v := &Visor{conf: conf}

	_, err = v.SetConfigFields(map[string]json.RawMessage{"transport.transport_port": json.RawMessage(`7777`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not file-backed")
}

func TestConfigFieldChangeString(t *testing.T) {
	require.Equal(t, "is_public: false -> true (live)",
		ConfigFieldChange{Path: "is_public", Old: []byte("false"), New: []byte("true"), Live: true}.String())
	require.Equal(t, "transport.transport_port: 0 -> 7777 (restart-required)",
		ConfigFieldChange{Path: "transport.transport_port", Old: []byte("0"), New: []byte("7777")}.String())
}
