// Package commands cmd/apps/exchange-market/commands/auth.go
package commands

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// The operator panel is authenticated with a one-time code the market itself
// mints and publishes to the visor, where it surfaces on the hypervisor's app
// list (which is behind hypervisor auth). The operator reads it there and types
// it into the market's login. On success the code is consumed and replaced, and
// the browser receives a short-lived token it keeps in memory only.
//
// Why no cookie: without an ambient credential a hostile page cannot make the
// browser replay it, so CSRF is structurally impossible rather than something
// we defend against. The cost is that a reload means a fresh code — deliberate.
const (
	// otpLen is the number of characters in a generated code — short enough to
	// read off a dashboard and retype, which is the whole ergonomic point.
	//
	// Five characters of otpAlphabet is only ~25 bits, so entropy alone does NOT
	// make this safe; two controls below do. Do not shorten it further without
	// revisiting them: at four characters the space is ~1M, which a few hundred
	// parallel source addresses would exhaust in hours.
	otpLen = 5

	// otpAlphabet excludes I/O/0/1 — the operator retypes these by eye from a
	// dashboard, so ambiguous glyphs cost more than the lost entropy.
	otpAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	// sessionTTL bounds how long a logged-in page may keep calling the API
	// before it must be re-authenticated with a fresh code.
	sessionTTL = 12 * time.Hour

	// loginBurst / loginRefill throttle login attempts per client IP. Budget is
	// consumed by every attempt, not just failures — the check necessarily runs
	// before the outcome is known. The burst is sized so a human operator
	// (one attempt, occasionally a typo) never notices.
	loginBurst  = 5
	loginRefill = time.Minute

	// globalLoginBurst / globalLoginRefill cap login attempts across ALL source
	// addresses. The per-IP limiter alone is useless against an attacker with a
	// botnet or an IPv6 range, which is exactly the threat a 25-bit code faces.
	globalLoginBurst  = 10
	globalLoginRefill = 6 * time.Second

	// otpFailureRotate replaces the code after this many failed attempts, so a
	// brute-force search can never converge: the target moves long before any
	// meaningful fraction of the space is explored. The operator just re-reads
	// the new code from the hypervisor app list.
	otpFailureRotate = 10
)

// authGate owns the market's current OTP and the set of live page sessions.
//
// The zero value is not usable; construct with newAuthGate.
type authGate struct {
	mu       sync.Mutex
	otp      string
	fails    int                  // consecutive failed attempts since the last rotation
	sessions map[string]time.Time // token -> expiry
	limiters map[string]*rate.Limiter
	global   *rate.Limiter
	// publish hands a freshly minted OTP to the visor. Injected so tests can
	// observe rotation without an app client.
	publish func(otp string)
}

// newAuthGate mints the first OTP and publishes it. publish may be nil.
func newAuthGate(publish func(otp string)) (*authGate, error) {
	if publish == nil {
		publish = func(string) {}
	}
	g := &authGate{
		sessions: make(map[string]time.Time),
		limiters: make(map[string]*rate.Limiter),
		global:   rate.NewLimiter(rate.Every(globalLoginRefill), globalLoginBurst),
		publish:  publish,
	}
	if err := g.rotate(); err != nil {
		return nil, err
	}
	return g, nil
}

// rotate replaces the current OTP and publishes the replacement. Callers must
// not hold g.mu.
func (g *authGate) rotate() error {
	otp, err := randomCode(otpLen)
	if err != nil {
		return err
	}

	g.mu.Lock()
	g.otp = otp
	g.fails = 0
	g.mu.Unlock()

	g.publish(otp)

	return nil
}

// login consumes the current OTP and returns a session token. The OTP is
// rotated on success, so a code works exactly once.
func (g *authGate) login(otp string) (string, bool) {
	// Normalize the way an operator retypes it: codes are displayed uppercase,
	// and browsers happily contribute stray whitespace.
	otp = strings.ToUpper(strings.TrimSpace(otp))

	g.mu.Lock()
	current := g.otp
	// Constant-time compare so a wrong code can't be narrowed by timing. The
	// length check is deliberately outside it — length is not a secret, and
	// subtle.ConstantTimeCompare returns 0 for mismatched lengths anyway.
	ok := len(otp) == len(current) &&
		subtle.ConstantTimeCompare([]byte(otp), []byte(current)) == 1
	if !ok {
		g.fails++
		exhausted := g.fails >= otpFailureRotate
		g.mu.Unlock()

		// Move the target before a search can converge. rotate() takes g.mu
		// itself, so this must happen after the unlock above.
		if exhausted {
			_ = g.rotate() //nolint:errcheck // keep the old code rather than lock the operator out
		}

		return "", false
	}
	g.mu.Unlock()

	token, err := randomCode(32)
	if err != nil {
		return "", false
	}

	g.mu.Lock()
	g.sessions[token] = time.Now().Add(sessionTTL)
	g.mu.Unlock()

	// Burn the used code. If minting a replacement fails the old one stays put
	// rather than leaving the panel unreachable; the operator can restart.
	if err := g.rotate(); err != nil {
		return token, true
	}

	return token, true
}

// valid reports whether token names a live session, extending it on use.
func (g *authGate) valid(token string) bool {
	if token == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	exp, ok := g.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(g.sessions, token)
		return false
	}
	g.sessions[token] = time.Now().Add(sessionTTL)

	return true
}

// allowLogin reports whether ip may attempt a login now, consuming budget from
// both limiters. It runs before the OTP is checked, so a successful attempt
// costs the same as a failed one.
//
// Two limiters apply: one per source IP, and one global. The global one is what
// actually protects a short code, since per-IP budget is trivially multiplied by
// an attacker with many addresses.
func (g *authGate) allowLogin(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.global.Allow() {
		return false
	}

	lim, ok := g.limiters[ip]
	if !ok {
		// Cap the table so a spoofed-source flood can't grow it without bound.
		if len(g.limiters) > 1024 {
			g.limiters = make(map[string]*rate.Limiter)
		}
		lim = rate.NewLimiter(rate.Every(loginRefill), loginBurst)
		g.limiters[ip] = lim
	}

	return lim.Allow()
}

// currentOTP is for tests and diagnostics. It is never served over HTTP.
func (g *authGate) currentOTP() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.otp
}

// randomCode returns n characters drawn uniformly from otpAlphabet using the
// CSPRNG. Rejection is unnecessary because len(otpAlphabet) is a power of two,
// but crypto/rand.Int is used anyway so the alphabet can change safely.
func randomCode(n int) (string, error) {
	max := big.NewInt(int64(len(otpAlphabet)))
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = otpAlphabet[idx.Int64()]
	}

	return string(b), nil
}

// clientIP extracts a rate-limiting key. X-Forwarded-For is deliberately
// ignored: it is attacker-controlled unless a trusted proxy is known to rewrite
// it, and trusting it would let one client evade the login limiter entirely.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// bearerToken pulls the session token from the Authorization header.
//
// A custom header, never HTTP Basic: browsers cache Basic credentials and
// resend them automatically, which would reintroduce the ambient credential
// this design exists to avoid.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}

	return ""
}

// loginHandler exchanges a valid OTP for a session token.
func (g *authGate) loginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			mWriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !g.allowLogin(clientIP(r)) {
			mWriteError(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
			return
		}

		var req struct {
			OTP string `json:"otp"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mWriteError(w, http.StatusBadRequest, "invalid login payload")
			return
		}

		token, ok := g.login(req.OTP)
		if !ok {
			mWriteError(w, http.StatusUnauthorized, "invalid or expired OTP")
			return
		}

		mWriteJSON(w, http.StatusOK, map[string]any{"token": token})
	}
}

// requireAuth wraps a handler so only requests carrying a live session token
// reach it.
func (g *authGate) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.valid(bearerToken(r)) {
			mWriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
