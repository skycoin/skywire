package visor

import (
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ui2FakeAPI answers the calls the logs and mail tabs make.
type ui2FakeAPI struct {
	visorapi.API
	mu       sync.Mutex
	logs     []string
	inbox    []skymail.Summary
	msg      *skymail.Rendered
	sent     *skymail.Outgoing
	sendRes  *skymail.SendResult
	deleted  string
	retried  *string
	wl       []cipher.PubKey
	settings *visorapi.MailSettingsUpdate
}

func (a *ui2FakeAPI) Apps() ([]*appserver.AppState, error) { return nil, nil }

func (a *ui2FakeAPI) SkychatPasswordIsSet() (bool, error) { return false, nil }

func (a *ui2FakeAPI) VoiceActive() ([]string, error) { return nil, ErrVoiceDisabled }

func (a *ui2FakeAPI) RuntimeLogsSince(since int64) (visorapi.RuntimeLogsDelta, error) {
	var d visorapi.RuntimeLogsDelta
	if int(since) < len(a.logs) {
		d.Entries = a.logs[since:]
	}
	d.Latest = int64(len(a.logs))
	return d, nil
}

func (a *ui2FakeAPI) MailStatus() (*visorapi.MailStatus, error) {
	return &visorapi.MailStatus{Enabled: true, Running: true, Address: "anyone@x.skynet", Unread: 1, Whitelist: a.wl}, nil
}

func (a *ui2FakeAPI) MailList(folder string) ([]skymail.Summary, error) {
	if folder == "INBOX" {
		return a.inbox, nil
	}
	return nil, nil
}

func (a *ui2FakeAPI) MailRead(_, id string) (*skymail.Rendered, error) {
	if a.msg == nil || id != "m1" {
		return nil, errors.New("no such message")
	}
	return a.msg, nil
}

func (a *ui2FakeAPI) MailSend(out skymail.Outgoing) (*skymail.SendResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = &out
	return a.sendRes, nil
}

func (a *ui2FakeAPI) MailDelete(_, id string) error { a.deleted = id; return nil }
func (a *ui2FakeAPI) MailRetry(id string) error     { a.retried = &id; return nil }
func (a *ui2FakeAPI) MailSetWhitelist(pks []cipher.PubKey) error {
	a.wl = pks
	return nil
}
func (a *ui2FakeAPI) MailSetSettings(u visorapi.MailSettingsUpdate) error {
	a.settings = &u
	return nil
}

func newUI2FakeServer(t *testing.T, api visorapi.API) string {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	hv := &Hypervisor{
		c:        visorconfig.HypervisorConfig{PK: pk},
		visor:    &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, opts: Options{NoCSRF: true}},
		mu:       new(sync.RWMutex),
		selfConn: Conn{API: api},
	}
	r := httputil.NewRouter()
	hv.ui2Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL + "/visors/" + pk.Hex()
}

func ui2HX(t *testing.T, method, u, ct, body string) (int, string) {
	t.Helper()
	req := ui2Req(t, method, u, ct, body)
	req.Header.Set("HX-Request", "true")
	return ui2Do(t, req)
}

// TestUI2Logs renders the buffer filtered and appends only what is new.
func TestUI2Logs(t *testing.T) {
	api := &ui2FakeAPI{logs: []string{
		`{"time":"t1","level":"info","msg":"hello","_module":"dmsg"}`,
		`{"time":"t2","level":"error","msg":"[2026-11-03T10:00:00Z] ERROR boom","_module":"router","error":"bad"}`,
		`not json`,
	}}
	base := newUI2FakeServer(t, api)

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/logs", "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "hello")
	require.Contains(t, body, ">boom<", "the duplicated logrus prefix is cut")
	require.Contains(t, body, "error=bad")
	require.Contains(t, body, "since=3")

	_, body = ui2HX(t, http.MethodGet, base+"/logs/list?level=6", "", "")
	require.NotContains(t, body, "hello")
	require.Contains(t, body, "boom")
	_, body = ui2HX(t, http.MethodGet, base+"/logs/list?module=^dmsg", "", "")
	require.Contains(t, body, "hello")
	require.NotContains(t, body, "boom")
	_, body = ui2HX(t, http.MethodGet, base+"/logs/list?module=(", "", "")
	require.Contains(t, body, `class="error"`, "a bad regex is shown, not a 500")

	_, body = ui2HX(t, http.MethodGet, base+"/logs/list?paused=1", "", "")
	require.NotContains(t, body, "every 2s", "a paused list does not poll")

	api.logs = append(api.logs, `{"time":"t4","level":"warning","msg":"late"}`)
	code, body = ui2HX(t, http.MethodGet, base+"/logs/more?since=3", "", "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `hx-swap-oob="beforeend:#loglist"`)
	require.Contains(t, body, "late")
	require.NotContains(t, body, "hello", "only new lines come back")
	require.Contains(t, body, "since=4")

	_, body = ui2HX(t, http.MethodGet, base+"/logs/more?since=4", "", "")
	require.NotContains(t, body, "beforeend", "nothing new, nothing appended")
}

// TestUI2Mail lists, reads, replies, sends with an attachment, deletes and
// saves the lists.
func TestUI2Mail(t *testing.T) {
	api := &ui2FakeAPI{
		inbox:   []skymail.Summary{{ID: "m1", From: "bob@y.skynet", Subject: "Hi there", FromVerified: true}},
		msg:     &skymail.Rendered{From: "bob@y.skynet", To: "me@x.skynet", Subject: "Hi there", Text: "line one\nline two", MessageID: "<m1@y>", Attachments: []skymail.Attachment{{Name: "a.txt", Size: 3}}},
		sendRes: &skymail.SendResult{Recipients: []skymail.RcptResult{{Rcpt: "bob@y.skynet", Via: "skynet"}}},
	}
	base := newUI2FakeServer(t, api)

	code, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/mail", "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Hi there")
	require.Contains(t, body, "anyone@x.skynet")
	require.Regexp(t, `Inbox\s*\(1\)`, body)

	_, body = ui2HX(t, http.MethodGet, base+"/mail/message?folder=INBOX&id=m1", "", "")
	require.Contains(t, body, "line two")
	require.Contains(t, body, `href="api/visors/`)
	require.Contains(t, body, "/mail/INBOX/m1/attachments/0")
	require.NotContains(t, body, "<html", "an htmx request gets the tab body only")

	_, body = ui2HX(t, http.MethodGet, base+"/mail/compose?folder=INBOX&reply=m1", "", "")
	require.Contains(t, body, `value="Re: Hi there"`)
	require.Contains(t, body, "&gt; line one")
	require.Contains(t, body, `value="&lt;m1@y&gt;"`)

	var mp strings.Builder
	mw := multipart.NewWriter(&mp)
	require.NoError(t, mw.WriteField("to", "bob@y.skynet, carol@z.skynet"))
	require.NoError(t, mw.WriteField("subject", "s"))
	require.NoError(t, mw.WriteField("body", "b"))
	fw, err := mw.CreateFormFile("files", "n.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte("note"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	code, body = ui2HX(t, http.MethodPost, base+"/mail/send", mw.FormDataContentType(), mp.String())
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "Sent.")
	require.Equal(t, []string{"bob@y.skynet", "carol@z.skynet"}, api.sent.To)
	require.Len(t, api.sent.Attachments, 1)
	require.Equal(t, "note", string(api.sent.Attachments[0].Data))

	api.sendRes = &skymail.SendResult{Recipients: []skymail.RcptResult{{Rcpt: "bob@y.skynet", Err: "not whitelisted"}}}
	form := url.Values{}
	mp.Reset()
	mw = multipart.NewWriter(&mp)
	require.NoError(t, mw.WriteField("to", "bob@y.skynet"))
	require.NoError(t, mw.WriteField("body", "keep me"))
	require.NoError(t, mw.Close())
	_, body = ui2HX(t, http.MethodPost, base+"/mail/send", mw.FormDataContentType(), mp.String())
	require.Contains(t, body, "not whitelisted")
	require.Contains(t, body, "keep me", "a failed send keeps the draft")

	_, body = ui2HX(t, http.MethodDelete, base+"/mail/message?folder=INBOX&id=m1", "", "")
	require.Equal(t, "m1", api.deleted)
	require.Contains(t, body, "Message deleted.")

	_, _ = ui2HX(t, http.MethodPost, base+"/mail/retry?folder=Outbox", "", "")
	require.NotNil(t, api.retried)
	require.Empty(t, *api.retried, "no id retries the whole Outbox")

	pk, _ := cipher.GenerateKeyPair()
	form.Set("pks", pk.Hex())
	_, body = ui2HX(t, http.MethodPost, base+"/mail/whitelist", "application/x-www-form-urlencoded", form.Encode())
	require.Equal(t, []cipher.PubKey{pk}, api.wl)
	require.Contains(t, body, pk.Hex())
	form.Set("pks", "nope")
	_, body = ui2HX(t, http.MethodPost, base+"/mail/whitelist", "application/x-www-form-urlencoded", form.Encode())
	require.Contains(t, body, `class="show err"`)

	hf := url.Values{"helpers": {""}, "relay_for": {"none"}}
	_, _ = ui2HX(t, http.MethodPost, base+"/mail/helpers", "application/x-www-form-urlencoded", hf.Encode())
	require.True(t, api.settings.HelpersDefault)
	require.NotNil(t, api.settings.RelayFor)
	require.Empty(t, *api.settings.RelayFor)
}

// TestUI2Islands hosts the existing pages by relative URL.
func TestUI2Islands(t *testing.T) {
	base := newUI2FakeServer(t, &ui2FakeAPI{})
	pk := base[strings.LastIndex(base, "/")+1:]
	for tab, src := range map[string]string{
		"terminal": `src="pty/` + pk + `"`,
		"wallet":   `src="wallet/config"`,
	} {
		code, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/"+tab, "", ""))
		require.Equal(t, http.StatusOK, code, tab)
		require.Contains(t, body, src, tab)
		require.Contains(t, body, `href="route-viz"`)
		require.NotContains(t, body, `href="tp-viz/"`, "no visualizer without tp-viz")
	}
}

func TestUI2CatalogPage(t *testing.T) {
	srv, _, pk := newUI2Server(t, true)
	code, body := ui2Do(t, ui2Req(t, http.MethodGet, srv.URL+"/visors/"+pk.Hex()+"/apps/catalog", "", ""))
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `id="catalog"`)
	require.Contains(t, body, `hx-get="visors/`+pk.Hex()+`/apps/catalog/body"`)
}

// TestUI2WalletHostsSkycoinWeb frames skycoin-web's own wallet once the app
// serves it, and says what to do while it does not.
func TestUI2WalletHostsSkycoinWeb(t *testing.T) {
	base := newUI2FakeServer(t, &ui2FakeAPI{})
	_, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/wallet", "", ""))
	require.Contains(t, body, "Skycoin wallet app not configured")
	require.NotContains(t, body, `src="wallet/ui2/"`)
	require.NotContains(t, body, `#/wallet`, "no link to the Angular wallet")

	launcher.RegisterHTTPHandler(skyenv.SkycoinWebName, http.NotFoundHandler())
	t.Cleanup(func() { launcher.RegisterHTTPHandler(skyenv.SkycoinWebName, nil) })
	_, body = ui2Do(t, ui2Req(t, http.MethodGet, base+"/wallet", "", ""))
	require.Contains(t, body, `src="wallet/ui2/"`)
}

// TestUI2ChatIsNative renders the chat tab without the Angular UI.
func TestUI2ChatIsNative(t *testing.T) {
	base := newUI2FakeServer(t, &ui2FakeAPI{})
	_, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/chat", "", ""))
	require.Contains(t, body, `id="chat-form"`)
	require.NotContains(t, body, "<iframe")
}

type ui2ProxyAPI struct {
	ui2FakeAPI
	enabled  map[string]bool
	upstream string
}

func (a *ui2ProxyAPI) EmbeddedProxies() (*visorapi.EmbeddedProxiesStatus, error) {
	return &visorapi.EmbeddedProxiesStatus{
		DmsgWeb:   &visorapi.EmbeddedProxyInfo{Running: a.enabled["dmsg"], SocksAddr: "127.0.0.1:4445"},
		SkynetWeb: &visorapi.EmbeddedProxyInfo{Running: a.enabled["skynet"], UpstreamSOCKS: a.upstream},
	}, nil
}

func (a *ui2ProxyAPI) SetEmbeddedProxyEnabled(kind string, on bool) error {
	a.enabled[kind] = on
	return nil
}

func (a *ui2ProxyAPI) SetEmbeddedProxyUpstream(_, addr string) error {
	a.upstream = addr
	return nil
}

func TestUI2WebProxy(t *testing.T) {
	api := &ui2ProxyAPI{enabled: map[string]bool{}}
	base := newUI2FakeServer(t, api)
	_, body := ui2Do(t, ui2Req(t, http.MethodGet, base+"/web-proxy", "", ""))
	require.Contains(t, body, "127.0.0.1:4445")
	form := "application/x-www-form-urlencoded"
	_, body = ui2HX(t, http.MethodPut, base+"/web-proxy/enabled", form, "on=1")
	require.True(t, api.enabled["skynet"] && api.enabled["dmsg"], "both resolvers switch together")
	require.Contains(t, body, `id="wp"`)
	_, _ = ui2HX(t, http.MethodPut, base+"/web-proxy/upstream", form, "addr=127.0.0.1:1080")
	require.Equal(t, "127.0.0.1:1080", api.upstream)
}
