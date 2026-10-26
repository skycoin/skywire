package visor

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

type mailSendAPI struct {
	visorapi.API
	res   *skymail.SendResult
	err   error
	delay time.Duration
}

func (a mailSendAPI) MailSend(skymail.Outgoing) (*skymail.SendResult, error) {
	time.Sleep(a.delay)
	return a.res, a.err
}

// A delivery that outlasts the server's WriteTimeout still gets its answer.
// A dropped reply reads as a failed send, and the user sends the mail again.
func TestMailSendOutlastsWriteTimeout(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		visor:    &Visor{opts: Options{NoCSRF: true}},
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: mailSendAPI{res: &skymail.SendResult{MessageID: "<x@y.dmsg>"}, delay: 300 * time.Millisecond}},
	}
	r := httputil.NewRouter()
	hv.mailRoutes(r)
	srv := httptest.NewUnstartedServer(r)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/visors/"+pk.Hex()+"/mail/send", "application/json",
		strings.NewReader(`{"to":["bob@z.dmsg"],"body":"x"}`))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck,gosec
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// A send that reached nobody still names each recipient and why, so a
// client can show which address failed rather than a bare error.
func TestMailSendFailureKeepsTheReasons(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	res := &skymail.SendResult{
		MessageID:  "<x@y.dmsg>",
		Recipients: []skymail.RcptResult{{Rcpt: "bob@z.dmsg", Err: "sender PK not whitelisted by this mailbox"}},
	}
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		visor:    &Visor{opts: Options{NoCSRF: true}},
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: mailSendAPI{res: res, err: errors.New("skymail: not delivered to any recipient")}},
	}
	r := httputil.NewRouter()
	hv.mailRoutes(r)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/visors/"+pk.Hex()+"/mail/send",
		strings.NewReader(`{"to":["bob@z.dmsg"],"body":"x"}`)))

	require.Equal(t, http.StatusBadGateway, rr.Code)
	var body struct {
		Error      string               `json:"error"`
		Recipients []skymail.RcptResult `json:"recipients"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Contains(t, body.Error, "not delivered")
	require.Len(t, body.Recipients, 1)
	require.Contains(t, body.Recipients[0].Err, "not whitelisted")
}
