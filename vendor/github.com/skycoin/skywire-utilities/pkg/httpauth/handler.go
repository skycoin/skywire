// Package httpauth pkg/httpauth/handler.go
package httpauth

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var (
	logger = logging.MustGetLogger("Auth")

	// ContextAuthKey stores authenticated PubKey in Context .
	ContextAuthKey = struct{}{}
	// LogAuthKey stores authentication PubKey in log entry
	LogAuthKey = "PK"
)

// HTTPResponse represents the http response struct
type HTTPResponse struct {
	Error *HTTPError  `json:"error,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

// HTTPError is included in an HTTPResponse
type HTTPError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// implements http.ResponseWriter
type statusWriter struct {
	http.ResponseWriter
	http.Hijacker
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}

	n, err := w.ResponseWriter.Write(b)

	return n, err
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("http.ResponseWriter does not implement http.Hijacker")
	}

	return hijacker.Hijack()
}

// WithAuth wraps a http.Handler and adds authentication logic.
// The original http.Handler is responsible for setting the status code.
// The middleware logic should only increment the security nonce if the status code
// from the original http.Handler is of 2xx value (representing success).
// Any http.Handler that is wrapped with this function will have available the authenticated
// public key from it's context, stored in the value ContextAuthKey.
func WithAuth(store NonceStore, original http.Handler, shouldVerifyAuth bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, err := AuthFromHeaders(r.Header, shouldVerifyAuth)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusUnauthorized,
				NewHTTPErrorResponse(http.StatusUnauthorized,
					err.Error()))
			return
		}

		if shouldVerifyAuth {
			err = verifyAuth(store, r, auth)
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusUnauthorized,
					NewHTTPErrorResponse(http.StatusUnauthorized,
						err.Error()))
				return
			}
		}

		sw := statusWriter{ResponseWriter: w}
		httputil.LogEntrySetField(r, LogAuthKey, auth.Key)
		original.ServeHTTP(&sw, r.WithContext(context.WithValue(
			r.Context(), ContextAuthKey, auth.Key)))

		if sw.status == http.StatusOK {
			_, err := store.IncrementNonce(r.Context(), auth.Key)
			if err != nil {
				logger.Error(err)
			}
		}
	})
}

func makeMiddleware(store NonceStore, shouldVerifyAuth bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return WithAuth(store, next, shouldVerifyAuth)
	}
}

// MakeMiddleware is a convenience function that calls WithAuth.
func MakeMiddleware(store NonceStore) func(next http.Handler) http.Handler {
	return makeMiddleware(store, true)
}

// PKFromCtx is a convenience function to obtain PK from ctx.
func PKFromCtx(ctx context.Context) cipher.PubKey {
	pk, _ := ctx.Value(ContextAuthKey).(cipher.PubKey)
	return pk
}

// MakeLoadTestingMiddleware is the same as `MakeMiddleware` but omits auth checks to simplify load testing.
func MakeLoadTestingMiddleware(store NonceStore) func(next http.Handler) http.Handler {
	return makeMiddleware(store, false)
}

// NextNonceResponse represents a ServeHTTP response for json encoding
type NextNonceResponse struct {
	Edge      cipher.PubKey `json:"edge"`
	NextNonce Nonce         `json:"next_nonce"`
}

// NonceHandler provides server-side logic for Skywire-related RESTFUL authorization and authentication.
type NonceHandler struct {
	Store NonceStore
}

// ServeHTTP implements http Handler
// Use this in endpoint:
// mux.Handle("/security/nonces/", &NonceHandler{Store})
func (as *NonceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remotePK, err := retrievePkFromURL(r.URL)
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest,
			NewHTTPErrorResponse(http.StatusBadRequest,
				err.Error()))

		return
	}

	var nilPK cipher.PubKey

	if remotePK == nilPK {
		httputil.WriteJSON(w, r, http.StatusBadRequest,
			NewHTTPErrorResponse(http.StatusBadRequest,
				"Invalid public key"))

		return
	}

	nonce, err := as.Store.Nonce(r.Context(), remotePK)
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusInternalServerError,
			NewHTTPErrorResponse(http.StatusInternalServerError,
				err.Error()))

		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, NextNonceResponse{Edge: remotePK, NextNonce: nonce})
}

// NewHTTPErrorResponse returns an HTTPResponse with the Error field populated
func NewHTTPErrorResponse(code int, msg string) HTTPResponse {
	if msg == "" {
		msg = http.StatusText(code)
	}

	return HTTPResponse{
		Error: &HTTPError{
			Code:    code,
			Message: msg,
		},
	}
}

// retrievePkFromURL returns the id used on endpoints of the form path/:pk
// it doesn't checks if the endpoint has this form and can fail with other
// endpoint forms
func retrievePkFromURL(url *url.URL) (cipher.PubKey, error) {
	splitPath := strings.Split(url.EscapedPath(), "/")
	v := splitPath[len(splitPath)-1]
	pk := cipher.PubKey{}
	err := pk.UnmarshalText([]byte(v))
	return pk, err
}

// GetRemoteAddr gets the remote address from the request
// in case of dmsghttp the RemoteAddress is a pk so it gets the RemoteAddr
// from the header instead
func GetRemoteAddr(r *http.Request) string {
	var pk cipher.PubKey

	// remove the port incase of an IP or a PK
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	err = pk.Set(host)
	if err == nil {
		return r.Header.Get("SW-PublicIP")
	}

	return host
}
