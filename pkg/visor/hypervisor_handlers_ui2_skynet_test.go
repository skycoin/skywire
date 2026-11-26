package visor

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/ui2"
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

var (
	ui2SkipRE    = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<span class="mi[^"]*">[^<]*</span>`)
	ui2TextRE    = regexp.MustCompile(`>([^<]+)<|(?:placeholder|title|aria-label|data-label|hx-confirm)="([^"]*)"`)
	ui2EnglishRE = regexp.MustCompile(`[\p{L}\p{N}]+`)
)

// ui2EnglishWords lists the English words a user reads on a page, leaving
// out the product names, language names and loanwords a translated page
// still shows.
func ui2EnglishWords(body string, allowed ...string) []string {
	ok := map[string]bool{"Skywire": true, "Skynet": true, "skynet": true, "DMSG": true, "dmsg": true,
		"English": true, "Español": true, "Deutsch": true, "Português": true, "Brazil": true, "localhost": true,
		"Web": true, "VPN": true}
	for _, w := range allowed {
		ok[w] = true
	}
	var out []string
	for _, m := range ui2TextRE.FindAllStringSubmatch(ui2SkipRE.ReplaceAllString(body, ""), -1) {
		for _, w := range ui2EnglishRE.FindAllString(m[1]+" "+m[2], -1) {
			if len(w) >= 3 && strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" && !ok[w] {
				out = append(out, w)
			}
		}
	}
	return out
}

// TestUI2SkynetZH renders the skynet tab in Chinese with nothing in English
// left, as a user reported it with ports and placeholders untranslated.
func TestUI2SkynetZH(t *testing.T) {
	api := &ui2SkynetAPI{fwds: map[uuid.UUID]*appnet.RawTCPForwardConn{}}
	base := newUI2FakeServer(t, api)
	get := func() string {
		req := ui2Req(t, http.MethodGet, base+"/skynet", "", "")
		req.AddCookie(&http.Cookie{Name: ui2.LangCookie, Value: "zh", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		code, body := ui2Do(t, req)
		require.Equal(t, http.StatusOK, code, body)
		return body
	}
	body := get()
	require.Contains(t, body, "没有转发的端口。")
	require.Contains(t, body, `placeholder="远程公钥"`)
	require.Empty(t, ui2EnglishWords(body))

	api.ports = []visorapi.ForwardedPort{{Port: 8080, Label: "web", Skynet: true}}
	body = get()
	require.Contains(t, body, "所有人")
	require.Empty(t, ui2EnglishWords(body, "web"))
}

// TestUI2PagesZH renders other tabs the fake API can serve in Chinese. Log
// level names stay as the log lines write them.
func TestUI2PagesZH(t *testing.T) {
	api := &ui2SkynetAPI{fwds: map[uuid.UUID]*appnet.RawTCPForwardConn{}}
	base := newUI2FakeServer(t, api)
	for _, tab := range []string{"/apps", "/logs", "/vpn"} {
		req := ui2Req(t, http.MethodGet, base+tab, "", "")
		req.AddCookie(&http.Cookie{Name: ui2.LangCookie, Value: "zh", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		code, body := ui2Do(t, req)
		require.Equal(t, http.StatusOK, code, tab)
		require.Empty(t, ui2EnglishWords(body, "Debug", "Info", "Warn", "Error", "Fatal", "Panic", "vpn", "client", "server"), tab)
	}
}
