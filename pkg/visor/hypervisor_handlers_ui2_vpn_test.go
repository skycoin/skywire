package visor

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
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
