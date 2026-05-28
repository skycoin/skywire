// Package visor pkg/visor/hypervisor_test.go
package visor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/usermanager"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// nolint: gosec
const (
	goodPayload             = `{"username":"admin","password":"Secure1234!"}`
	changePasswordPayload   = `{"old_password":"Secure1234!","new_password":"NewSecure1234!"}`
	changedPasswordPayload  = `{"username":"admin","password":"NewSecure1234!"}`
	badCreateAccountPayload = `{"username":"invalid_user","password":"Secure1234!"}`
)

func TestNewNode(t *testing.T) {
	config := visorconfig.MakeConfig(false)
	config.EnableAuth = true
	config.FillDefaults(false)

	confDir := os.TempDir()

	config.DBPath = filepath.Join(confDir, "users_test.db")

	t.Run("no_access_without_login", func(t *testing.T) {
		testNodeNoAccessWithoutLogin(t, config)
	})

	t.Run("only_admin_account_allowed", func(t *testing.T) {
		testNodeOnlyAdminAccountAllowed(t, config)
	})

	t.Run("cannot_login_twice", func(t *testing.T) {
		testNodeCannotLoginTwice(t, config)
	})

	t.Run("access_after_login", func(t *testing.T) {
		testNodeAccessAfterLogin(t, config)
	})

	t.Run("no_access_after_logout", func(t *testing.T) {
		testNodeNoAccessAfterLogout(t, config)
	})

	t.Run("change_password", func(t *testing.T) {
		testNodeChangePassword(t, config)
	})
}

func makeStartNode(t *testing.T, config visorconfig.HypervisorConfig) (string, *http.Client, func()) {
	defaultMockConfig := MockConfig{
		Visors:            5,
		MaxTpsPerVisor:    10,
		MaxRoutesPerVisor: 10,
		EnableAuth:        true,
	}

	visor, err := NewHypervisor(config, nil, nil)
	require.NoError(t, err)
	require.NoError(t, visor.AddMockData(defaultMockConfig))

	srv := httptest.NewTLSServer(visor.HTTPHandler())
	visor.c.Cookies.Domain = srv.Listener.Addr().String()

	client := srv.Client()
	jar, err := cookiejar.New(&cookiejar.Options{})
	require.NoError(t, err)

	client.Jar = jar

	return srv.Listener.Addr().String(), client, func() {
		srv.Close()
		_ = visor.users.Close() // nolint:errcheck
		require.NoError(t, os.Remove(config.DBPath))
	}
}

type TestCase struct {
	ReqMethod  string
	ReqURI     string
	ReqBody    io.Reader
	ReqMod     func(req *http.Request)
	RespStatus int
	RespBody   func(t *testing.T, resp *http.Response)
}

func testCases(t *testing.T, addr string, client *http.Client, cases []TestCase) {
	for i, tc := range cases {
		testTag := fmt.Sprintf("[%d] %s", i, tc.ReqURI)

		testCase(t, addr, client, tc, testTag)
	}
}

func testCase(t *testing.T, addr string, client *http.Client, tc TestCase, testTag string) {
	req, err := http.NewRequest(tc.ReqMethod, "https://"+addr+tc.ReqURI, tc.ReqBody)
	require.NoError(t, err, testTag)

	if tc.ReqMod != nil {
		tc.ReqMod(req)
	}

	resp, err := client.Do(req)

	require.NoError(t, err, testTag)

	defer func() {
		assert.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, tc.RespStatus, resp.StatusCode, testTag)

	if tc.RespBody != nil {
		tc.RespBody(t, resp)
	}
}

func testNodeNoAccessWithoutLogin(t *testing.T, config visorconfig.HypervisorConfig) {
	addr, client, stop := makeStartNode(t, config)
	defer stop()

	makeCase := func(method string, uri string, body io.Reader) TestCase {
		return TestCase{
			ReqMethod:  method,
			ReqURI:     uri,
			ReqBody:    body,
			RespStatus: http.StatusUnauthorized,
			RespBody: func(t *testing.T, r *http.Response) {
				body, err := decodeErrorBody(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, usermanager.ErrBadSession.Error(), body.Error)
			},
		}
	}

	testCases(t, addr, client, []TestCase{
		makeCase(http.MethodGet, "/api/user", nil),
		makeCase(http.MethodPost, "/api/change-password", strings.NewReader(`{"old_password":"old","new_password":"new"}`)),
		makeCase(http.MethodGet, "/api/visors", nil),
	})
}

func testNodeOnlyAdminAccountAllowed(t *testing.T, config visorconfig.HypervisorConfig) {
	addr, client, stop := makeStartNode(t, config)
	defer stop()

	testCases(t, addr, client, []TestCase{
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/create-account",
			ReqBody:    strings.NewReader(badCreateAccountPayload),
			RespStatus: http.StatusForbidden,
			RespBody: func(t *testing.T, r *http.Response) {
				body, err := decodeErrorBody(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, usermanager.ErrNameNotAllowed.Error(), body.Error)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/create-account",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
	})
}

func testNodeCannotLoginTwice(t *testing.T, config visorconfig.HypervisorConfig) {
	addr, client, stop := makeStartNode(t, config)
	defer stop()

	testCases(t, addr, client, []TestCase{
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/create-account",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusForbidden,
			RespBody: func(t *testing.T, r *http.Response) {
				body, err := decodeErrorBody(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, usermanager.ErrNotLoggedOut.Error(), body.Error)
			},
		},
	})
}

func testNodeAccessAfterLogin(t *testing.T, config visorconfig.HypervisorConfig) {
	addr, client, stop := makeStartNode(t, config)
	defer stop()

	testCases(t, addr, client, []TestCase{
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/create-account",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodGet,
			ReqURI:     "/api/user",
			RespStatus: http.StatusOK,
		},
		{
			ReqMethod:  http.MethodGet,
			ReqURI:     "/api/visors",
			RespStatus: http.StatusOK,
		},
	})
}

func testNodeNoAccessAfterLogout(t *testing.T, config visorconfig.HypervisorConfig) {
	addr, client, stop := makeStartNode(t, config)
	defer stop()

	testCases(t, addr, client, []TestCase{
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/create-account",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/logout",
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodGet,
			ReqURI:     "/api/user",
			RespStatus: http.StatusUnauthorized,
			RespBody: func(t *testing.T, r *http.Response) {
				body, err := decodeErrorBody(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, usermanager.ErrBadSession.Error(), body.Error)
			},
		},
		{
			ReqMethod:  http.MethodGet,
			ReqURI:     "/api/visors",
			RespStatus: http.StatusUnauthorized,
			RespBody: func(t *testing.T, r *http.Response) {
				body, err := decodeErrorBody(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, usermanager.ErrBadSession.Error(), body.Error)
			},
		},
	})
}

// - Create account.
// - Login.
// - Change Password.
// - Attempt action (should fail).
// - Logout.
// - Login with old password (should fail).
// - Login with new password (should succeed).
// nolint: funlen
func testNodeChangePassword(t *testing.T, config visorconfig.HypervisorConfig) {
	addr, client, stop := makeStartNode(t, config)
	defer stop()

	// To emulate an active session.
	var cookies []*http.Cookie

	testCases(t, addr, client, []TestCase{
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/create-account",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				cookies = r.Cookies()
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/change-password",
			ReqBody:    strings.NewReader(changePasswordPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod: http.MethodGet,
			ReqURI:    "/api/visors",
			ReqMod: func(req *http.Request) {
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
			},
			RespStatus: http.StatusUnauthorized,
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/logout",
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(goodPayload),
			RespStatus: http.StatusUnauthorized,
			RespBody: func(t *testing.T, r *http.Response) {
				b, err := decodeErrorBody(r.Body)
				assert.NoError(t, err)
				require.Equal(t, usermanager.ErrBadLogin.Error(), b.Error)
			},
		},
		{
			ReqMethod:  http.MethodPost,
			ReqURI:     "/api/login",
			ReqBody:    strings.NewReader(changedPasswordPayload),
			RespStatus: http.StatusOK,
			RespBody: func(t *testing.T, r *http.Response) {
				var ok bool
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&ok))
				assert.True(t, ok)
			},
		},
	})
}

type ErrorBody struct {
	Error string `json:"error"`
}

func decodeErrorBody(rb io.Reader) (*ErrorBody, error) {
	b := new(ErrorBody)
	dec := json.NewDecoder(rb)
	dec.DisallowUnknownFields()

	return b, dec.Decode(b)
}

func TestPKEndpoint(t *testing.T) {
	const callerPKHex = "030000000000000000000000000000000000000000000000000000000000000001"

	t.Run("default_enabled_with_header_returns_pk", func(t *testing.T) {
		config := visorconfig.MakeConfig(false)
		config.EnableAuth = false
		config.FillDefaults(false)
		config.DBPath = filepath.Join(os.TempDir(), "users_pk_default.db")

		addr, client, closeFn := makeStartNode(t, config)
		defer closeFn()

		req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/api/pk", nil)
		require.NoError(t, err)
		req.Header.Set("SW-Public", callerPKHex)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body struct {
			PublicKey string `json:"public_key"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Len(t, body.PublicKey, 66, "public_key should be 66 hex chars")
		require.Equal(t, config.PK.Hex(), body.PublicKey)
	})

	t.Run("missing_sw_public_returns_401", func(t *testing.T) {
		config := visorconfig.MakeConfig(false)
		config.EnableAuth = false
		config.FillDefaults(false)
		config.DBPath = filepath.Join(os.TempDir(), "users_pk_noheader.db")

		addr, client, closeFn := makeStartNode(t, config)
		defer closeFn()

		req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/api/pk", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("malformed_sw_public_returns_400", func(t *testing.T) {
		config := visorconfig.MakeConfig(false)
		config.EnableAuth = false
		config.FillDefaults(false)
		config.DBPath = filepath.Join(os.TempDir(), "users_pk_malformed.db")

		addr, client, closeFn := makeStartNode(t, config)
		defer closeFn()

		req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/api/pk", nil)
		require.NoError(t, err)
		req.Header.Set("SW-Public", "notapubkey")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("disabled_returns_404", func(t *testing.T) {
		config := visorconfig.MakeConfig(false)
		config.EnableAuth = false
		config.DisablePKEndpoint = true
		config.FillDefaults(false)
		config.DBPath = filepath.Join(os.TempDir(), "users_pk_disabled.db")

		addr, client, closeFn := makeStartNode(t, config)
		defer closeFn()

		req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/api/pk", nil)
		require.NoError(t, err)
		req.Header.Set("SW-Public", callerPKHex)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint:errcheck

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}
