// Package httputil pkg/httputil/router_test.go
package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func say(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(s + r.PathValue("pk") + r.PathValue("rest"))) //nolint:errcheck,gosec
	}
}

func tag(s string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-Mw", s)
			next.ServeHTTP(w, r)
		})
	}
}

func TestRouter(t *testing.T) {
	rt := NewRouter()
	rt.Use(tag("root"))
	rt.Get("/a/", say("a-slash"))
	rt.Route("/api", func(r *Router) {
		r.Get("/", say("api"))
		r.Get("/v/{pk}", say("v"))
		r.Group(func(r *Router) {
			r.Use(tag("auth"))
			r.Post("/v/{pk}", say("post"))
		})
	})
	rt.With(tag("with")).Get("/with", say("with"))
	rt.Mount("/m", http.StripPrefix("/m", say("m")))
	rt.Handle("/files/{rest...}", say("f"))
	rt.Route("/", func(r *Router) {
		r.Route("/deep", func(r *Router) { r.Get("/x", say("deep")) })
	})
	rt.Handle("/{rest...}", say("ui"))

	cases := []struct{ method, path, body string }{
		{"GET", "/a/", "a-slash"},
		{"GET", "/api", "api"},
		{"GET", "/api/", "api"},
		{"GET", "/api/v/02ab", "v02ab"},
		{"POST", "/api/v/02ab", "post02ab"},
		{"GET", "/with", "with"},
		{"GET", "/m", "m"},
		{"GET", "/m/x/y", "m"},
		{"PUT", "/files/x/y", "fx/y"},
		{"GET", "/deep/x", "deep"},
	}
	for _, c := range cases {
		w := do(rt, c.method, c.path)
		require.Equal(t, http.StatusOK, w.Code, c.path)
		require.Equal(t, c.body, w.Body.String(), c.path)
		require.Equal(t, "root", w.Header().Values("X-Mw")[0], c.path)
	}
	require.Equal(t, []string{"root", "auth"}, do(rt, "POST", "/api/v/1").Header().Values("X-Mw"))
	require.Equal(t, []string{"root"}, do(rt, "GET", "/api/v/1").Header().Values("X-Mw"))
	require.Equal(t, []string{"root", "with"}, do(rt, "GET", "/with").Header().Values("X-Mw"))

	require.Equal(t, "uia/b", do(rt, "GET", "/a/b").Body.String(), "a trailing slash matches only itself")
	w := do(rt, "GET", "/api/v/1/x")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, "root", w.Header().Get("X-Mw"), "root middleware runs for unmatched paths")
	require.Equal(t, http.StatusMethodNotAllowed, do(rt, "DELETE", "/api/v/1").Code)
	require.Equal(t, http.StatusNotFound, do(rt, "GET", "/api/nope").Code, "Route owns its prefix")
	require.Equal(t, http.StatusNotFound, do(rt, "GET", "/deep").Code, "a bare Route prefix is not redirected")
	w = do(rt, "PUT", "/api/v/1")
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	require.Equal(t, "GET, POST", w.Header().Get("Allow"))
	require.Equal(t, "uiother/page", do(rt, "GET", "/other/page").Body.String())

	r, pattern := TrackRoutePattern(httptest.NewRequest("POST", "/api/v/1", nil))
	rt.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, "/api/v/{pk}", *pattern)

	require.Panics(t, func() { rt.Get("/old/*", say("x")) })
	require.Panics(t, func() { rt.Get("/api/v/{pk}", say("dup")) }, "ServeMux rejects a duplicate")
}

func TestTimeout(t *testing.T) {
	h := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	require.Equal(t, http.StatusGatewayTimeout, do(h, "GET", "/").Code)
}

func TestRealIPAndRequestID(t *testing.T) {
	var remote, id string
	h := RealIP(RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		remote, id = r.RemoteAddr, GetReqID(r.Context())
	})))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, "203.0.113.7", remote)
	require.NotEmpty(t, id)

	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "not-an-ip")
	req.Header.Set("X-Request-Id", "given")
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, "192.0.2.1:1234", remote, "an invalid header leaves RemoteAddr alone")
	require.Equal(t, "given", id)
}

func TestCORS(t *testing.T) {
	h := CORS(CORSOptions{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET", "POST"}, ExposedHeaders: []string{"Link"}, MaxAge: 300})(say("ok"))
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "https://x.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "300", w.Header().Get("Access-Control-Max-Age"))
	require.Empty(t, w.Body.String())

	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://x.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, "ok", w.Body.String())
	require.Equal(t, "Link", w.Header().Get("Access-Control-Expose-Headers"))
}

func TestRateLimit(t *testing.T) {
	h := RateLimit(2, time.Hour, KeyByRemoteAddr)(say("ok"))
	require.Equal(t, http.StatusOK, do(h, "GET", "/").Code)
	require.Equal(t, http.StatusOK, do(h, "GET", "/").Code)
	require.Equal(t, http.StatusTooManyRequests, do(h, "GET", "/").Code)

	other := httptest.NewRequest("GET", "/", nil)
	other.RemoteAddr = "[2001:db8::1]:5"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, other)
	require.Equal(t, http.StatusOK, w.Code, "each key has its own budget")
	require.Equal(t, "2001:db8::", KeyByRemoteAddr(other))
}
