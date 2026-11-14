package visor

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

type ui2AppsAPI struct {
	ui2FakeAPI
	apps    map[string]*appserver.AppState
	started []string
}

func (a *ui2AppsAPI) Apps() ([]*appserver.AppState, error) {
	out := []*appserver.AppState{}
	for _, s := range a.apps {
		out = append(out, s)
	}
	return out, nil
}

func (a *ui2AppsAPI) App(name string) (*appserver.AppState, error) { return a.apps[name], nil }

func (a *ui2AppsAPI) StartApp(name string) error {
	a.started = append(a.started, name)
	a.apps[name].Status = appserver.AppStatusRunning
	return nil
}

func (a *ui2AppsAPI) SetAppDetailedStatus(string, string) error { return nil }

func (a *ui2AppsAPI) SetAutoStart(name string, on bool) error {
	a.apps[name].AutoStart = on
	return nil
}

func TestUI2AppsFilterAndBulk(t *testing.T) {
	st := func(n string) *appserver.AppState {
		return &appserver.AppState{AppConfig: appserver.AppConfig{Name: n}}
	}
	api := &ui2AppsAPI{apps: map[string]*appserver.AppState{"skychat": st("skychat"), "vpn-client": st("vpn-client"), "mine": st("mine")}}
	base := newUI2FakeServer(t, api)

	_, body := ui2HX(t, http.MethodGet, base+"/apps/rows?f_name=vpn", "", "")
	require.Contains(t, body, `value="vpn-client"`)
	require.NotContains(t, body, `value="skychat"`)

	form := url.Values{"action": {"start"}, "sel": {"skychat", "mine"}}
	_, _ = ui2HX(t, http.MethodPost, base+"/apps/bulk", "application/x-www-form-urlencoded", form.Encode())
	require.ElementsMatch(t, []string{"skychat", "mine"}, api.started)

	form = url.Values{"action": {"auto-on"}, "sel": {"vpn-client"}, "f_state": {"stopped"}}
	_, body = ui2HX(t, http.MethodPost, base+"/apps/bulk", "application/x-www-form-urlencoded", form.Encode())
	require.True(t, api.apps["vpn-client"].AutoStart)
	require.NotContains(t, body, `value="skychat"`, "the answer keeps the filter")
}
