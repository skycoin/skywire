package visor

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/alerts"
)

type ui2AlertsAPI struct {
	ui2FakeAPI
	st alerts.Status
}

func (a *ui2AlertsAPI) AlertsStatus() (*alerts.Status, error) {
	s := a.st
	return &s, nil
}

func (a *ui2AlertsAPI) AlertsSet(s alerts.Settings) error {
	if s.SMTP != nil && s.SMTP.Password == "" && a.st.Settings.SMTP != nil {
		s.SMTP.PasswordSet = a.st.Settings.SMTP.PasswordSet
	}
	a.st.Settings = s
	a.st.OfflineAfter = time.Duration(s.OfflineAfter).String()
	return nil
}

func (a *ui2AlertsAPI) AlertsTest() ([]alerts.Result, error) {
	return []alerts.Result{{Channel: "webhook", Target: "http://x/hook"}}, nil
}

func (a *ui2AlertsAPI) AlertsPushAdd(sub alerts.PushSubscription) (alerts.PushSubscription, error) {
	sub.ID = alerts.PushID(sub.Endpoint)
	a.st.Settings.Push = append(a.st.Settings.Push, sub)
	return sub, nil
}

func TestUI2AlertsPushAdd(t *testing.T) {
	api := &ui2AlertsAPI{st: alerts.Status{OfflineAfter: "5m0s", PushPublicKey: "BKey"}}
	base := newUI2FakeServer(t, api)
	form := "application/x-www-form-urlencoded"

	_, body := ui2HX(t, http.MethodGet, base+"/settings/alerts", "", "")
	require.Contains(t, body, `data-push-key="BKey"`)

	sub := `{"endpoint":"https://push.example/abc","keys":{"p256dh":"p","auth":"a"},"label":"Linux Chrome"}`
	_, body = ui2HX(t, http.MethodPost, base+"/settings/alerts/push", form, url.Values{"sub": {sub}}.Encode())
	require.Len(t, api.st.Settings.Push, 1)
	require.Equal(t, "a", api.st.Settings.Push[0].Keys.Auth)
	require.Contains(t, body, `data-push-endpoint="https://push.example/abc"`)
	require.Contains(t, body, "This device gets push alerts now")

	_, body = ui2HX(t, http.MethodPost, base+"/settings/alerts/push", form, url.Values{"sub": {"{"}}.Encode())
	require.Contains(t, body, `class="error"`)
}

func TestUI2Alerts(t *testing.T) {
	api := &ui2AlertsAPI{st: alerts.Status{OfflineAfter: "5m0s"}}
	base := newUI2FakeServer(t, api)
	form := "application/x-www-form-urlencoded"

	_, body := ui2HX(t, http.MethodPut, base+"/settings/alerts", form, url.Values{"emails": {"nope"}, "delay": {"5m0s"}}.Encode())
	require.Contains(t, body, `&#34;nope&#34; is not an email address`)
	require.Contains(t, body, `value="nope"`, "the form keeps what was typed")

	v := url.Values{"emails": {"a@b.c, d@e.f"}, "delay": {"15m0s"}, "webhook": {"https://h/x"}, "relay": {"1"}, "host": {"smtp.x"}, "port": {"587"}, "starttls": {"1"}}
	_, body = ui2HX(t, http.MethodPut, base+"/settings/alerts", form, v.Encode())
	require.Equal(t, []string{"a@b.c", "d@e.f"}, api.st.Settings.EmailTo)
	require.Equal(t, "smtp.x", api.st.Settings.SMTP.Host)
	require.Equal(t, 15*time.Minute, time.Duration(api.st.Settings.OfflineAfter))
	require.Contains(t, body, `<option value="15m0s" selected>`)

	_, body = ui2HX(t, http.MethodPost, base+"/settings/alerts/test", form, "")
	require.Contains(t, body, "http://x/hook")
}
