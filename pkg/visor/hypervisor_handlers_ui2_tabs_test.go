package visor

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const ui2Form = "application/x-www-form-urlencoded"

// TestUI2TabsRelative keeps the new tabs' URLs relative, as TestUI2RelativeURLs
// does for contacts.
func TestUI2TabsRelative(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	abs := regexp.MustCompile(`(?:href|src|hx-get|hx-post|hx-put|hx-delete)="/`)
	for _, tab := range []string{"uptime", "rewards", "settings"} {
		code, body := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/visors/"+pk.Hex()+"/"+tab, "", ""))
		require.Equal(t, http.StatusOK, code, tab)
		require.Empty(t, abs.FindAllString(body, -1), tab)
		require.Contains(t, body, `class="tab active" href="ui2/visors/`+pk.Hex()+`/`+tab+`"`, tab)
	}
}

// TestUI2Rewards sets, refuses and removes a reward address.
func TestUI2Rewards(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	base := srv.URL + "/ui2/visors/" + pk.Hex() + "/rewards"

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base, "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Not registered")

	code, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/address?edit=1", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `hx-put="ui2/visors/`+pk.Hex()+`/rewards/address"`)

	code, body = ui2Do(t, ui2Req(t, http.MethodPut, base+"/address", ui2Form, url.Values{"address": {"2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"}}.Encode()))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "valid account xpub", "a plain address is refused")
	require.Contains(t, body, "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "the form keeps what was typed")

	code, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/rules", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "<pre")
}

// TestUI2Settings reviews and saves a skywire.conf change, and refuses a value
// the visor would not accept.
func TestUI2Settings(t *testing.T) {
	srv, v, pk := newUI2Server(t, true)
	v.conf.Skyenv = filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(v.conf.Skyenv, []byte("#!/usr/bin/env bash\n"), 0o600))
	base := srv.URL + "/ui2/visors/" + pk.Hex() + "/settings"

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base, "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `name="v.MINDMSGSESS"`)
	require.NotContains(t, body, `name="v.SK"`, "the secret key is never offered")

	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base+"/review", ui2Form, url.Values{"v.MINDMSGSESS": {"many"}}.Encode()))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "is not a whole number")
	require.Contains(t, body, `value="many"`)

	change := url.Values{"v.MINDMSGSESS": {"3"}}.Encode()
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base+"/review", ui2Form, change))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "MINDMSGSESS")
	require.Contains(t, body, `hx-put="ui2/visors/`+pk.Hex()+`/settings"`)

	code, body = ui2Do(t, ui2Req(t, http.MethodPut, base, ui2Form, change))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Saved to")
	conf, err := os.ReadFile(v.conf.Skyenv)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(conf), "MINDMSGSESS=3") || strings.Contains(string(conf), "MINDMSGSESS='3'") || strings.Contains(string(conf), `MINDMSGSESS="3"`), string(conf))

	code, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/panel", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Regexp(t, `name="v.MINDMSGSESS" value="3"`, body)
}

// TestUI2Uptime serves the tab and its window choices.
func TestUI2Uptime(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	code, body := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/visors/"+pk.Hex()+"/uptime/bars?days=30", "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `hx-get="ui2/visors/`+pk.Hex()+`/uptime/bars?days=30"`)
	require.Contains(t, body, `hx-trigger="every 60s`)
}
