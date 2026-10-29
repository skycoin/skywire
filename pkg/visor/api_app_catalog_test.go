package visor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func catalogState(name, binary string, port uint16, status appserver.AppStatus) *appserver.AppState {
	ac := appserver.AppConfig{Name: name, Binary: binary, AutoStart: true, Port: routing.Port(port)}
	return &appserver.AppState{AppConfig: ac, Status: status}
}

func TestBuildAppCatalog(t *testing.T) {
	registered := []string{"skychat", "skysocks", "skysocks-client"}
	states := []*appserver.AppState{
		catalogState("skychat", "", 1, appserver.AppStatusRunning),
		catalogState("skysocks-client", "skysocks-client", 13, appserver.AppStatusStopped),
		catalogState("skysocks-client-2", "skysocks-client", 14, appserver.AppStatusRunning),
		catalogState("skycoin-daemon", "skywire", 60, appserver.AppStatusRunning),
	}
	got := buildAppCatalog(registered, states)

	byName := map[string]visorapi.CatalogEntry{}
	var names []string
	for _, e := range got {
		byName[e.Name] = e
		names = append(names, e.Name)
	}
	require.Equal(t, []string{"skychat", "skycoin-daemon", "skysocks", "skysocks-client"}, names)

	chat := byName["skychat"]
	require.Equal(t, launcher.KindService, chat.Kind)
	require.NotEmpty(t, chat.Description)
	require.Equal(t, []visorapi.CatalogInstance{{Name: "skychat", Port: 1, AutoStart: true, Status: "running"}}, chat.Instances)

	srv := byName["skysocks"]
	require.Equal(t, launcher.KindServer, srv.Kind)
	require.NotNil(t, srv.Instances)
	require.Empty(t, srv.Instances)

	cli := byName["skysocks-client"]
	require.Equal(t, launcher.KindClient, cli.Kind)
	require.True(t, cli.MultiInstance)
	require.Len(t, cli.Instances, 2)
	require.Equal(t, "skysocks-client-2", cli.Instances[1].Name)
	require.Equal(t, "running", cli.Instances[1].Status)

	ext := byName["skycoin-daemon"]
	require.Equal(t, launcher.KindExternal, ext.Kind)
	require.Len(t, ext.Instances, 1)
	require.Equal(t, uint16(60), ext.Instances[0].Port)
}

// TestBuildAppCatalogUnknownRegistered keeps a registered app with no
// metadata in the catalog as a service.
func TestBuildAppCatalogUnknownRegistered(t *testing.T) {
	got := buildAppCatalog([]string{"new-app"}, nil)
	require.Len(t, got, 1)
	require.Equal(t, launcher.KindService, got[0].Kind)
	require.Empty(t, got[0].Instances)
}

type catalogAPI struct {
	visorapi.API
	entries []visorapi.CatalogEntry
}

func (c catalogAPI) AppCatalog() ([]visorapi.CatalogEntry, error) { return c.entries, nil }

func TestAppCatalogRoute(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	want := []visorapi.CatalogEntry{{Name: "skysocks", Kind: launcher.KindServer, Instances: []visorapi.CatalogInstance{}}}
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: catalogAPI{entries: want}},
	}
	r := httputil.NewRouter()
	r.Get("/visors/{pk}/apps/catalog", hv.getAppCatalog())
	r.Get("/visors/{pk}/apps/{app}", hv.getApp())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/visors/" + pk.Hex() + "/apps/catalog")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck,gosec
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	var got []visorapi.CatalogEntry
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, want, got)
}
