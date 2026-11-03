package visor

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUI2TabsB loads the apps, transports and routing tabs of a visor that
// has none of the subsystems running: each page renders, with the error in
// the page rather than a broken response.
func TestUI2TabsB(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	base := srv.URL + "/ui2/visors/" + pk.Hex()
	for _, p := range []string{
		"/apps", "/apps/rows",
		"/transports", "/transports/body",
		"/routing", "/routing?view=groups", "/routing/min-hops", "/routing/min-hops?edit=1",
		"/routing/rules", "/routing/groups", "/routing/policies", "/routing/find", "/routing/calc",
	} {
		code, body := ui2Do(t, ui2Req(t, http.MethodGet, base+p, "", ""))
		require.Equal(t, http.StatusOK, code, p+": "+body)
		require.NotContains(t, body, `="/`, p+": absolute URL")
	}
	_, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/routing/find", "", ""))
	require.Contains(t, body, `hx-post="ui2/visors/`+pk.Hex()+`/routing/find"`)
	_, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/transports", "", ""))
	require.Contains(t, body, `class="tab active"`)
	require.Contains(t, body, "Create transport")
}

// TestUI2TransportsInput turns down a bad key before calling the visor.
func TestUI2TransportsInput(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	form := url.Values{"remote": {"nope"}, "type": {"dmsg"}}.Encode()
	code, _ := ui2Do(t, ui2Req(t, http.MethodPost, srv.URL+"/ui2/visors/"+pk.Hex()+"/transports", "application/x-www-form-urlencoded", form))
	require.Equal(t, http.StatusBadRequest, code)
}

// TestUI2MinHopsInput keeps a bad value in the form with the error.
func TestUI2MinHopsInput(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	form := url.Values{"min": {"x"}}.Encode()
	code, body := ui2Do(t, ui2Req(t, http.MethodPost, srv.URL+"/ui2/visors/"+pk.Hex()+"/routing/min-hops", "application/x-www-form-urlencoded", form))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `class="error"`)
	require.Contains(t, body, `value="x"`)
}
