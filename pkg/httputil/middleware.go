// Package httputil pkg/httputil/middleware.go
package httputil

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RealIP sets r.RemoteAddr from True-Client-IP, X-Real-IP or the first
// X-Forwarded-For address, when one holds a valid IP.
func RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ip string
		switch {
		case r.Header.Get("True-Client-IP") != "":
			ip = r.Header.Get("True-Client-IP")
		case r.Header.Get("X-Real-IP") != "":
			ip = r.Header.Get("X-Real-IP")
		case r.Header.Get("X-Forwarded-For") != "":
			ip, _, _ = strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
		}
		if ip = strings.TrimSpace(ip); net.ParseIP(ip) != nil {
			r.RemoteAddr = ip
		}
		next.ServeHTTP(w, r)
	})
}

type requestIDKey struct{}

var (
	reqIDPrefix = func() string {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "localhost"
		}
		var b [12]byte
		rand.Read(b[:]) //nolint:errcheck,gosec
		s := strings.NewReplacer("+", "", "/", "").Replace(base64.StdEncoding.EncodeToString(b[:]))
		return host + "/" + s[:10]
	}()
	reqIDCount atomic.Uint64
)

// RequestID gives each request an ID, the X-Request-Id header when the client
// sent one. GetReqID reads it back.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = fmt.Sprintf("%s-%06d", reqIDPrefix, reqIDCount.Add(1))
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// GetReqID returns the request ID that RequestID stored in ctx, or "".
func GetReqID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WithReqID returns ctx carrying a request ID, for tests.
func WithReqID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// Timeout cancels the request context after d and answers 504 when the
// handler returns on that deadline. Unlike http.TimeoutHandler it does not
// buffer the response, so streaming and hijacking still work.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer func() {
				cancel()
				if ctx.Err() == context.DeadlineExceeded {
					w.WriteHeader(http.StatusGatewayTimeout)
				}
			}()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORSOptions configures CORS.
type CORSOptions struct {
	AllowedOrigins []string // "*" allows any
	AllowedMethods []string
	AllowedHeaders []string
	ExposedHeaders []string
	MaxAge         int // seconds a preflight may be cached
}

// CORS answers preflight requests and adds CORS headers to the others.
func CORS(o CORSOptions) Middleware {
	anyOrigin := false
	for _, v := range o.AllowedOrigins {
		anyOrigin = anyOrigin || v == "*"
	}
	allowed := func(origin string) bool {
		if anyOrigin {
			return true
		}
		for _, v := range o.AllowedOrigins {
			if strings.EqualFold(v, origin) {
				return true
			}
		}
		return false
	}
	allowOrigin := func(h http.Header, origin string) {
		if anyOrigin {
			h.Set("Access-Control-Allow-Origin", "*")
			return
		}
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
	}
	methods := strings.Join(o.AllowedMethods, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if origin != "" && allowed(origin) {
					h := w.Header()
					allowOrigin(h, origin)
					h.Set("Access-Control-Allow-Methods", methods)
					if len(o.AllowedHeaders) > 0 {
						h.Set("Access-Control-Allow-Headers", strings.Join(o.AllowedHeaders, ", "))
					}
					if o.MaxAge > 0 {
						h.Set("Access-Control-Max-Age", strconv.Itoa(o.MaxAge))
					}
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			if origin != "" && allowed(origin) {
				allowOrigin(w.Header(), origin)
				if len(o.ExposedHeaders) > 0 {
					w.Header().Set("Access-Control-Expose-Headers", strings.Join(o.ExposedHeaders, ", "))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyByRemoteAddr keys a rate limit by the peer IP, an IPv6 peer by its /64.
func KeyByRemoteAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String()
	}
	return host
}

// RateLimit allows limit requests per window for each key and answers 429
// past that. It estimates a sliding window from the current and previous
// fixed windows, as go-chi/httprate does.
func RateLimit(limit int, window time.Duration, key func(*http.Request) string) Middleware {
	type counts struct{ cur, prev int }
	var (
		mu    sync.Mutex
		start = time.Now().Truncate(window)
		byKey = map[string]*counts{}
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := time.Now()
			k := key(r)
			mu.Lock()
			if cur := now.Truncate(window); cur != start {
				adjacent := cur.Sub(start) == window
				for kk, c := range byKey {
					if adjacent && c.cur > 0 {
						c.prev, c.cur = c.cur, 0
					} else {
						delete(byKey, kk)
					}
				}
				start = cur
			}
			c := byKey[k]
			if c == nil {
				c = &counts{}
				byKey[k] = c
			}
			elapsed := float64(now.Sub(start)) / float64(window)
			rate := int(math.Round(float64(c.prev)*(1-elapsed))) + c.cur
			ok := rate < limit
			if ok {
				c.cur++
			}
			remaining := max(limit-rate-1, 0)
			mu.Unlock()

			h := w.Header()
			h.Set("X-RateLimit-Limit", strconv.Itoa(limit))
			h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			h.Set("X-RateLimit-Reset", strconv.FormatInt(start.Add(window).Unix(), 10))
			if !ok {
				h.Set("Retry-After", strconv.Itoa(int(time.Until(start.Add(window)).Seconds())+1))
				http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
