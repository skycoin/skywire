package visor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// The Chat tab of a remote visor must not show the local visor's chat.
func TestSkychatProxyLocalVsRemote(t *testing.T) {
	local, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()

	skychat := http.NewServeMux()
	skychat.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("local-chat")) //nolint:errcheck
	})
	launcher.RegisterHTTPHandler(skyenv.SkychatName, skychat)
	defer launcher.ClearHTTPHandler(skyenv.SkychatName, skychat)

	hv := &Hypervisor{
		c:     visorconfig.HypervisorConfig{PK: local},
		visor: &Visor{opts: Options{NoCSRF: true}},
		mu:    new(sync.RWMutex),
	}
	r := httputil.NewRouter()
	r.HandleFunc("/visors/{pk}/skychat/proxy/{rest...}", hv.skychatProxyHandler())

	get := func(pk cipher.PubKey) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/visors/"+pk.Hex()+"/skychat/proxy/messages", nil))
		return rr
	}

	rr := get(local)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "local-chat", rr.Body.String())

	rr = get(remote)
	require.Equal(t, http.StatusNotImplemented, rr.Code)
	require.NotContains(t, rr.Body.String(), "local-chat")
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Contains(t, body.Error, "remote visor")
}
