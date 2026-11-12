package visor

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type ui2SkynetAPI struct {
	ui2FakeAPI
	ports []visorapi.ForwardedPort
	fwds  map[uuid.UUID]*appnet.RawTCPForwardConn
}

func (a *ui2SkynetAPI) ListForwardedPorts() ([]visorapi.ForwardedPort, error) { return a.ports, nil }

func (a *ui2SkynetAPI) RegisterForwardedPort(p visorapi.ForwardedPort) error {
	a.ports = append(a.ports, p)
	return nil
}

func (a *ui2SkynetAPI) UpdateForwardedPort(p visorapi.ForwardedPort) error {
	for i := range a.ports {
		if a.ports[i].Port == p.Port {
			a.ports[i] = p
		}
	}
	return nil
}

func (a *ui2SkynetAPI) DeregisterTCPPort(port int) error {
	out := a.ports[:0]
	for _, p := range a.ports {
		if p.Port != port {
			out = append(out, p)
		}
	}
	a.ports = out
	return nil
}

func (a *ui2SkynetAPI) ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error) {
	return a.fwds, nil
}

func TestUI2Skynet(t *testing.T) {
	api := &ui2SkynetAPI{fwds: map[uuid.UUID]*appnet.RawTCPForwardConn{}}
	base := newUI2FakeServer(t, api)
	form := "application/x-www-form-urlencoded"
	peer, _ := cipher.GenerateKeyPair()

	_, body := ui2HX(t, http.MethodPost, base+"/skynet/ports", form, url.Values{"port": {"8080"}, "label": {"web"}, "skynet": {"1"}, "whitelist": {"nope"}}.Encode())
	require.Contains(t, body, "Invalid public key: nope")
	require.Empty(t, api.ports)

	_, body = ui2HX(t, http.MethodPost, base+"/skynet/ports", form, url.Values{"port": {"8080"}, "label": {"web"}, "skynet": {"1"}, "whitelist": {peer.Hex()}}.Encode())
	require.Len(t, api.ports, 1)
	require.Equal(t, []cipher.PubKey{peer}, api.ports[0].Whitelist)
	require.Contains(t, body, "web")

	_, _ = ui2HX(t, http.MethodPost, base+"/skynet/toggle", form, "port=8080&field=dmsg")
	require.True(t, api.ports[0].DMSG)
	_, _ = ui2HX(t, http.MethodPost, base+"/skynet/whitelist", form, "port=8080&whitelist=")
	require.Empty(t, api.ports[0].Whitelist)
	_, _ = ui2HX(t, http.MethodPost, base+"/skynet/remove", form, "port=8080")
	require.Empty(t, api.ports)
}
