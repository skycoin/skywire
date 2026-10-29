package visor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands" // registers skysocks-client and its flags
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func flagByName(t *testing.T, flags []visorapi.AppFlag, name string) visorapi.AppFlag {
	t.Helper()
	for _, f := range flags {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("flag %q not in %+v", name, flags)
	return visorapi.AppFlag{}
}

// TestRegisteredAppFlagsSkysocksClient reads the real skysocks-client flag
// set through a second instance that names it as its binary.
func TestRegisteredAppFlagsSkysocksClient(t *testing.T) {
	ac := appserver.AppConfig{
		Name:   "skysocks-client-2",
		Binary: "skysocks-client",
		Args:   []string{"--srv", "02aa", "--reconnect", "--tunnels=3", "--addr=:1090"},
	}
	flags, ok := registeredAppFlags(ac)
	require.True(t, ok)
	applyArgValues(flags, ac.Args)

	for _, f := range flags {
		require.NotContains(t, []string{"help", "version", "port"}, f.Name)
	}

	srv := flagByName(t, flags, "srv")
	require.Equal(t, visorapi.FlagTypeString, srv.Type)
	require.Equal(t, autoconfigcmd.CheckPK, srv.Check)
	require.True(t, srv.Set)
	require.Equal(t, "02aa", srv.Value)

	rc := flagByName(t, flags, "reconnect")
	require.Equal(t, visorapi.FlagTypeBool, rc.Type)
	require.Equal(t, "false", rc.Default)
	require.True(t, rc.Set)
	require.Equal(t, "true", rc.Value)

	tun := flagByName(t, flags, "tunnels")
	require.Equal(t, visorapi.FlagTypeInt, tun.Type)
	require.Equal(t, "3", tun.Value)

	rs := flagByName(t, flags, "range-split")
	require.Equal(t, "true", rs.Default)
	require.False(t, rs.Set)

	require.Equal(t, autoconfigcmd.CheckHostPort, flagByName(t, flags, "addr").Check)
}

func TestRegisteredAppFlagsUnknown(t *testing.T) {
	_, ok := registeredAppFlags(appserver.AppConfig{Name: "skycoin-daemon", Binary: "skywire", Args: []string{"skycoin", "daemon"}})
	require.False(t, ok)
}

const sampleCobraHelp = `skycoin daemon

Usage:
  skywire skycoin daemon [flags]

Flags:
  -c, --config string            path to the config file
      --data-dir string          directory to store app data (default "$HOME/.skycoin")
      --enable-all-api-sets      enable every API set
      --log-level string         log level (default "info")
      --max-connections int      maximum connections (default 128)
      --peers strings            extra peers to dial (default [a:1,b:2])
      --timeout duration         dial timeout (default 10s)
      --wrapped string           a usage that the terminal
                                 wrapped onto a second line
  -h, --help                     help for daemon
      --port int                 routing port

Global Flags:
      --quiet   less output
`

func TestParseHelpFlagsCobra(t *testing.T) {
	flags := parseHelpFlags(sampleCobraHelp)
	names := make([]string, 0, len(flags))
	for _, f := range flags {
		names = append(names, f.Name)
	}
	require.Equal(t, []string{"config", "data-dir", "enable-all-api-sets", "log-level", "max-connections", "peers", "timeout", "wrapped", "quiet"}, names)

	cfg := flagByName(t, flags, "config")
	require.Equal(t, "c", cfg.Shorthand)
	require.Equal(t, "", cfg.Default)

	require.Equal(t, visorapi.AppFlag{Name: "data-dir", Type: "string", Default: "$HOME/.skycoin", Usage: "directory to store app data"}, flagByName(t, flags, "data-dir"))
	require.Equal(t, visorapi.AppFlag{Name: "enable-all-api-sets", Type: "bool", Default: "false", Usage: "enable every API set"}, flagByName(t, flags, "enable-all-api-sets"))
	require.Equal(t, "128", flagByName(t, flags, "max-connections").Default)
	require.Equal(t, visorapi.FlagTypeInt, flagByName(t, flags, "max-connections").Type)

	peers := flagByName(t, flags, "peers")
	require.Equal(t, visorapi.FlagTypeStringList, peers.Type)
	require.Equal(t, "a:1,b:2", peers.Default)

	to := flagByName(t, flags, "timeout")
	require.Equal(t, visorapi.FlagTypeDuration, to.Type)
	require.Equal(t, "10s", to.Default)
	require.Equal(t, autoconfigcmd.CheckDuration, to.Check)

	require.Equal(t, "a usage that the terminal wrapped onto a second line", flagByName(t, flags, "wrapped").Usage)

	applyArgValues(flags, []string{"skycoin", "daemon", "--peers", "x:1", "--peers=y:2", "-c", "/etc/sky.json", "--unknown", "1"})
	require.Equal(t, "x:1,y:2", flagByName(t, flags, "peers").Value)
	require.Equal(t, "/etc/sky.json", flagByName(t, flags, "config").Value)
	require.False(t, flagByName(t, flags, "timeout").Set)
}

func TestParseHelpFlagsGo(t *testing.T) {
	help := "Usage of tool:\n  -addr string\n    \tlisten address (default \":80\")\n  -v\tverbose output\n"
	flags := parseHelpFlags(help)
	require.Equal(t, []visorapi.AppFlag{
		{Name: "addr", Type: "string", Default: ":80", Usage: "listen address", Check: autoconfigcmd.CheckHostPort},
		{Name: "v", Type: "bool", Default: "false", Usage: "verbose output"},
	}, flags)
}

type flagsAPI struct {
	visorapi.API
	flags visorapi.AppFlags
}

func (f flagsAPI) App(name string) (*appserver.AppState, error) {
	return &appserver.AppState{AppConfig: appserver.AppConfig{Name: name}}, nil
}

func (f flagsAPI) AppFlags(string) (visorapi.AppFlags, error) { return f.flags, nil }

func TestAppFlagsRoute(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	want := visorapi.AppFlags{App: "skysocks", Source: visorapi.FlagSourceRegistry, Flags: []visorapi.AppFlag{
		{Name: "whitelist", Type: "string", Usage: "keys", Check: autoconfigcmd.CheckPKs, Value: "02aa", Set: true},
	}}
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: flagsAPI{flags: want}},
	}
	r := httputil.NewRouter()
	r.Get("/visors/{pk}/apps/{app}/flags", hv.appFlags())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/visors/" + pk.Hex() + "/apps/skysocks/flags")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck,gosec
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	var got visorapi.AppFlags
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, want, got)
}
