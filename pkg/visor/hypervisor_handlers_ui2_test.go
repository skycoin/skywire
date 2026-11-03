package visor

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func newUI2Server(t *testing.T, noCSRF bool) (*httptest.Server, *Visor, cipher.PubKey) {
	t.Helper()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, opts: Options{NoCSRF: noCSRF}}
	v.conf.LocalPath = filepath.Join(t.TempDir(), "local")
	pk, _ := cipher.GenerateKeyPair()
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		visor:    v,
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: v},
	}
	r := httputil.NewRouter()
	r.Get("/api/csrf", hv.getCsrf())
	hv.ui2Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, v, pk
}

func ui2Do(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := ui2Client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck,gosec
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

func ui2Req(t *testing.T, method, u, contentType, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

// TestUI2Contacts drives the htmx contacts tab: page, add, bad input, filter,
// edit, import, delete.
func TestUI2Contacts(t *testing.T) {
	srv, v, pk := newUI2Server(t, true)
	base := srv.URL + "/ui2/visors/" + pk.Hex() + "/contacts"
	form := "application/x-www-form-urlencoded"

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base, "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "No contacts yet.")
	require.Contains(t, body, pk.Hex(), "the page names the visor by its full key")

	add := url.Values{"name": {"Alice"}, "keys": {pk.Hex()}, "groups": {"work, friends"}, "payments": {"skycoin:2jBbGx"}}
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base, form, add.Encode()))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Contact saved.")
	require.Contains(t, body, `id="count"`)
	require.Contains(t, body, "friends, work", "the book sorts groups")

	list, err := v.ContactsList()
	require.NoError(t, err)
	require.Len(t, list, 1)
	alice := list[0]
	require.Equal(t, []cipher.PubKey{pk}, alice.Keys)
	require.Equal(t, "skycoin", alice.Payments[0].Coin)

	bad := url.Values{"name": {"Bob"}, "keys": {"not-a-key"}}
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base, form, bad.Encode()))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `class="error"`)
	require.Contains(t, body, "not-a-key", "the form keeps what was typed")

	code, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/rows?q=nobody", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "No contact matches")
	code, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/rows?q=FRIENDS", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "Alice")

	code, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/"+alice.UID+"/edit", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `value="Alice"`)
	require.Contains(t, body, "skycoin:2jBbGx")
	code, _ = ui2Do(t, ui2Req(t, http.MethodGet, base+"/nope/edit", "", ""))
	require.Equal(t, http.StatusNotFound, code)

	edit := url.Values{"uid": {alice.UID}, "name": {"Alice B"}, "keys": {pk.Hex()}}
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base, form, edit.Encode()))
	require.Equal(t, http.StatusOK, code, body)
	list, err = v.ContactsList()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Alice B", list[0].Name)

	vcf, err := v.ContactsExport()
	require.NoError(t, err)
	code, body = ui2Do(t, ui2Req(t, http.MethodDelete, base+"/"+alice.UID, "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "No contacts yet.")
	code, _ = ui2Do(t, ui2Req(t, http.MethodDelete, base+"/"+alice.UID, "", ""))
	require.Equal(t, http.StatusNotFound, code)

	var mp strings.Builder
	mw := multipart.NewWriter(&mp)
	fw, err := mw.CreateFormFile("vcf", "contacts.vcf")
	require.NoError(t, err)
	_, err = fw.Write(vcf)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base+"/import", mw.FormDataContentType(), mp.String()))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Imported 1 contact(s).")
	require.Contains(t, body, "Alice B")

	code, body = ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/static/htmx.min.js", "", ""))
	require.Equal(t, http.StatusOK, code)
	require.True(t, strings.HasPrefix(body, "var htmx="))
}

// TestUI2ContactsCSRF checks that the pages take the same CSRF token the
// JSON routes do.
func TestUI2ContactsCSRF(t *testing.T) {
	srv, _, pk := newUI2Server(t, false)
	base := srv.URL + "/ui2/visors/" + pk.Hex() + "/contacts"
	body := url.Values{"name": {"Carol"}}.Encode()

	code, _ := ui2Do(t, ui2Req(t, http.MethodPost, base, "application/x-www-form-urlencoded", body))
	require.Equal(t, http.StatusForbidden, code)

	_, tok := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/api/csrf", "", ""))
	m := regexp.MustCompile(`"csrf_token":"([^"]+)"`).FindStringSubmatch(tok)
	require.Len(t, m, 2, tok)
	req := ui2Req(t, http.MethodPost, base, "application/x-www-form-urlencoded", body)
	req.Header.Set(CSRFHeaderName, m[1])
	code, out := ui2Do(t, req)
	require.Equal(t, http.StatusOK, code, out)
	require.Contains(t, out, "Contact saved.")
}

// TestUI2RelativeURLs keeps every URL in the page relative. The in-tab visor's
// service worker serves it under a /vnet/<port>/ prefix the server never sees.
func TestUI2RelativeURLs(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	_, body := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/ui2/visors/"+pk.Hex()+"/contacts", "", ""))
	abs := regexp.MustCompile(`(?:href|src|hx-get|hx-post|hx-delete)="/`).FindAllString(body, -1)
	require.Empty(t, abs, "absolute URLs escape the /vnet/<port>/ prefix")
	require.Contains(t, body, `<base href="../../../">`)
}

// TestUI2RemoteVisor renders and writes the book of a visor connected to the
// hypervisor, not the hypervisor's own.
func TestUI2RemoteVisor(t *testing.T) {
	local := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, opts: Options{NoCSRF: true}}
	local.conf.LocalPath = filepath.Join(t.TempDir(), "local")
	remote := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}}
	remote.conf.LocalPath = filepath.Join(t.TempDir(), "remote")
	hvPK, _ := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair()
	_, err := remote.ContactsPut(contacts.Contact{Name: "Remote Dave"})
	require.NoError(t, err)
	hv := &Hypervisor{
		c:            visorconfig.HypervisorConfig{PK: hvPK},
		visor:        local,
		mu:           new(sync.RWMutex),
		selfConn:     Conn{API: local},
		remoteVisors: map[cipher.PubKey]Conn{remotePK: {API: remote}},
	}
	r := httputil.NewRouter()
	hv.ui2Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	base := srv.URL + "/ui2/visors/" + remotePK.Hex() + "/contacts"

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base, "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Remote Dave")

	add := url.Values{"name": {"Erin"}}.Encode()
	code, body = ui2Do(t, ui2Req(t, http.MethodPost, base, "application/x-www-form-urlencoded", add))
	require.Equal(t, http.StatusOK, code, body)
	got, err := remote.ContactsList()
	require.NoError(t, err)
	require.Len(t, got, 2)
	mine, err := local.ContactsList()
	require.NoError(t, err)
	require.Empty(t, mine, "the write went to the remote visor")
}
