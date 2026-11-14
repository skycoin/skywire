package visor

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type ui2VPNAPI struct{ ui2FakeAPI }

func (a *ui2VPNAPI) Apps() ([]*appserver.AppState, error) {
	return []*appserver.AppState{{AppConfig: appserver.AppConfig{Name: "vpn-client"}, Status: appserver.AppStatusRunning}}, nil
}

func TestUI2VPN(t *testing.T) {
	base := newUI2FakeServer(t, &ui2VPNAPI{})
	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/vpn", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `"run":"","view":"vpn"`, "the running client offers stop")
	require.Contains(t, body, "The vpn-server app is not configured on this visor.")
	require.NotContains(t, body, "<iframe")
}

type ui2VPNPickAPI struct {
	ui2VPNAPI
	args []string
}

func (a *ui2VPNPickAPI) AppFlags(string) (visorapi.AppFlags, error) {
	return visorapi.AppFlags{Flags: []visorapi.AppFlag{
		{Name: "srv", Type: "string", Set: true, Value: "old"},
		{Name: "dns", Type: "string", Set: true, Value: "1.1.1.1"},
		{Name: "killswitch", Type: "bool", Default: "false"},
	}}, nil
}

func (a *ui2VPNPickAPI) App(name string) (*appserver.AppState, error) {
	return &appserver.AppState{AppConfig: appserver.AppConfig{Name: name, Args: a.args}}, nil
}

func (a *ui2VPNPickAPI) RestartApp(string) error { return nil }

func (a *ui2VPNPickAPI) SetAppArgs(_ string, args []string) error {
	a.args = args
	return nil
}

func TestUI2VPNUse(t *testing.T) {
	api := &ui2VPNPickAPI{args: []string{"--srv", "old", "--dns", "1.1.1.1"}}
	base := newUI2FakeServer(t, api)
	srv, _ := cipher.GenerateKeyPair()
	code, body := ui2HX(t, http.MethodPost, base+"/vpn/use", "application/x-www-form-urlencoded", "app=vpn-client&srv="+srv.Hex())
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, []string{"--srv=" + srv.Hex(), "--dns=1.1.1.1"}, api.args, "other flags stay")
}
