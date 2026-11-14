package visor

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type ui2ChatPwAPI struct {
	ui2FakeAPI
	pw string
}

func (a *ui2ChatPwAPI) SkychatPasswordIsSet() (bool, error) { return a.pw != "", nil }

func (a *ui2ChatPwAPI) SetSkychatPassword(old, nw string) error {
	if a.pw != "" && old != a.pw {
		return errors.New("wrong password")
	}
	a.pw = nw
	return nil
}

func (a *ui2ChatPwAPI) ClearSkychatPassword(old string) error {
	if old != a.pw {
		return errors.New("wrong password")
	}
	a.pw = ""
	return nil
}

func TestUI2ChatPassword(t *testing.T) {
	api := &ui2ChatPwAPI{}
	base := newUI2FakeServer(t, api)
	form := "application/x-www-form-urlencoded"
	post := func(v url.Values) string {
		_, body := ui2HX(t, http.MethodPost, base+"/chat/password", form, v.Encode())
		return body
	}
	require.Contains(t, post(url.Values{"new": {"abc"}, "confirm": {"abc"}}), "6-64 characters")
	require.Contains(t, post(url.Values{"new": {"secret1"}, "confirm": {"secret2"}}), "Passwords do not match")
	body := post(url.Values{"new": {"secret1"}, "confirm": {"secret1"}})
	require.Equal(t, "secret1", api.pw)
	require.Contains(t, body, "Change password")
	require.Contains(t, post(url.Values{"clear": {"1"}}), "Current password is required")
	_ = post(url.Values{"clear": {"1"}, "old": {"secret1"}})
	require.Empty(t, api.pw)
}
