package visor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestContactsRoutes drives the dashboard's contact routes against a real book:
// add, list, export, delete, then import the export back.
func TestContactsRoutes(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, opts: Options{NoCSRF: true}}
	v.conf.LocalPath = filepath.Join(t.TempDir(), "local")
	pk, _ := cipher.GenerateKeyPair()
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		visor:    v,
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: v},
	}
	r := httputil.NewRouter()
	hv.contactsRoutes(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	base := srv.URL + "/visors/" + pk.Hex() + "/contacts"

	do := func(method, url, body string) (int, []byte) {
		req, err := http.NewRequest(method, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck,gosec
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, b
	}

	code, b := do(http.MethodPost, base, `{"name":"Alice","keys":["`+pk.Hex()+`"],"groups":["work"]}`)
	require.Equal(t, http.StatusOK, code, string(b))
	var alice contacts.Contact
	require.NoError(t, json.Unmarshal(b, &alice))
	require.NotEmpty(t, alice.UID)

	code, b = do(http.MethodPost, base, `{"name":""}`)
	require.Equal(t, http.StatusBadRequest, code, "a contact needs a name: %s", b)

	code, b = do(http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, code)
	var list []contacts.Contact
	require.NoError(t, json.Unmarshal(b, &list))
	require.Len(t, list, 1)
	require.Equal(t, []cipher.PubKey{pk}, list[0].Keys)

	code, vcf := do(http.MethodGet, base+"/export", "")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, string(vcf), "FN:Alice")

	code, _ = do(http.MethodDelete, base+"/"+alice.UID, "")
	require.Equal(t, http.StatusOK, code)
	code, _ = do(http.MethodDelete, base+"/"+alice.UID, "")
	require.Equal(t, http.StatusNotFound, code)

	code, b = do(http.MethodPost, base+"/import", string(vcf))
	require.Equal(t, http.StatusOK, code, string(b))
	require.JSONEq(t, `{"imported":1}`, string(b))

	code, _ = do(http.MethodPost, base+"/import", "BEGIN:VCARD\nnot a card")
	require.Equal(t, http.StatusBadRequest, code)
}
