package visor

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// TestUI2ChangePassword checks the settings page's password form against
// the account store, and that the page reads the refresh rate cookie.
func TestUI2ChangePassword(t *testing.T) {
	srv, _ := newUI2AuthServer(t)
	form := "application/x-www-form-urlencoded"
	login := func(pw string) []*http.Cookie {
		v := url.Values{"username": {"admin"}, "password": {pw}}
		resp, err := ui2Client.Do(ui2Req(t, http.MethodPost, srv.URL+"/login", form, v.Encode()))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		if resp.StatusCode != http.StatusSeeOther {
			return nil
		}
		return resp.Cookies()
	}
	create := url.Values{"create": {"1"}, "username": {"admin"}, "password": {"Secret123!"}, "confirm": {"Secret123!"}}
	resp, err := ui2Client.Do(ui2Req(t, http.MethodPost, srv.URL+"/login", form, create.Encode()))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	post := func(v url.Values) (*http.Response, string) {
		req := ui2Req(t, http.MethodPost, srv.URL+"/settings/password", form, v.Encode())
		req.Header.Set("HX-Request", "true")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := ui2Client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck,gosec
		var b [4096]byte
		n, _ := resp.Body.Read(b[:]) //nolint:errcheck
		return resp, string(b[:n])
	}

	req := ui2Req(t, http.MethodGet, srv.URL+"/settings", "", "")
	req.AddCookie(&http.Cookie{Name: ui2.RefreshCookie, Value: "30", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	code, body := ui2Do(t, req)
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, `<option value="30" selected>`)
	require.Contains(t, body, `hx-post="settings/password"`)

	_, body = post(url.Values{"old": {"Secret123!"}, "new": {"Another1!"}, "repeat": {"nope"}})
	require.Contains(t, body, "Passwords do not match.")
	_, body = post(url.Values{"old": {"wrong"}, "new": {"Another1!"}, "repeat": {"Another1!"}})
	require.Contains(t, body, "The provided old password is not correct.")
	resp, _ = post(url.Values{"old": {"Secret123!"}, "new": {"Another1!"}, "repeat": {"Another1!"}})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, "login", resp.Header.Get("HX-Redirect"))

	require.Nil(t, login("Secret123!"))
	require.NotNil(t, login("Another1!"))
}
