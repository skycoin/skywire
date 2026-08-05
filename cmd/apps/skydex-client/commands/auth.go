// Package commands cmd/apps/skydex-client/commands/auth.go
//
// HTTP basic-auth gate for the SkyDEX trading UI. Off by default; on when a
// password file is configured and non-empty. The file format is the one
// skychat already uses — a single line of "<hex-salt>:<hex-hash>", SHA256 over
// password||salt, the same scheme as the hypervisor's user store
// (pkg/visor/usermanager/user.go) — so one password-writing flow serves both
// surfaces.
//
// # Why this app needs a gate at all
//
// The trading UI has no authentication of its own, and everything behind it is
// live: the connected market session, the registered wallet addresses, placing
// and cancelling orders. On a desktop, a loopback listener is reachable only by
// the user's own login session and that is usually the end of it. On Android it
// is not: there is no per-app network namespace, so a listener on 127.0.0.1 is
// reachable by EVERY installed app holding INTERNET, with no prompt. Without a
// gate, "an app on the phone can trade your coins" is the accurate description.
//
// # What the gate can and cannot close
//
// The UI itself is served by the engine in the skycoin repo, whose Run() takes
// an address and builds its own listener — it accepts neither a net.Listener nor
// a handler this wrapper could wrap. So the gate is a reverse proxy in front:
// the configured --addr carries the gate, and the engine is moved to a loopback
// port drawn fresh at every start (see gatedServer).
//
// That fully closes the documented address, which is what an unprivileged
// caller actually has. It does NOT make the engine's own port unreachable — a
// process determined to scan loopback can still find it, and the operating
// system offers no way to bind a socket only this UID may connect to. Closing
// that last gap needs one change upstream: Run() accepting a net.Listener (or
// exposing its handler), at which point the proxy here collapses into wrapping
// a handler and no second port exists at all.
package commands

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	authMu           sync.RWMutex
	authPasswordSalt []byte
	authPasswordHash cipher.SHA256
	authPasswordSet  bool
)

// loadUIPassword reads "<hex-salt>:<hex-hash>" from path. An empty path, a
// missing file or an empty file all mean "no auth" — the desktop default.
// Errors other than ENOENT are returned; the caller logs and continues with the
// gate disabled rather than blocking startup on a transient FS hiccup.
func loadUIPassword(path string) error {
	authMu.Lock()
	defer authMu.Unlock()
	authPasswordSet = false
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return nil
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return nil // malformed — treat as no auth, log-handled by caller
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return err
	}
	hashBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		return err
	}
	if len(hashBytes) != len(authPasswordHash) {
		return nil
	}
	copy(authPasswordHash[:], hashBytes)
	authPasswordSalt = salt
	authPasswordSet = true
	return nil
}

// uiPasswordSet reports whether a usable password was loaded — i.e. whether
// there is anything to put a gate in front of.
func uiPasswordSet() bool {
	authMu.RLock()
	defer authMu.RUnlock()
	return authPasswordSet
}

// requireAuth wraps a handler with HTTP basic auth. With no password
// configured the wrapped handler runs unchanged, so this is safe to install
// unconditionally.
//
// The username is ignored, as it is in skychat: there is one credential, and a
// browser challenge has to send some name anyway.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.RLock()
		set := authPasswordSet
		salt := authPasswordSalt
		hash := authPasswordHash
		authMu.RUnlock()

		if !set {
			next.ServeHTTP(w, r)
			return
		}

		_, password, ok := r.BasicAuth()
		if !ok {
			unauthorized(w, "authentication required")
			return
		}
		if cipher.SumSHA256(append([]byte(password), salt...)) != hash {
			unauthorized(w, "invalid credentials")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="skydex-client"`)
	http.Error(w, "skydex-client: "+reason, http.StatusUnauthorized)
}

// gatedServer puts the password gate on uiAddr and returns the address the
// engine should serve on instead: a loopback port drawn fresh at every start,
// so the only address anyone can name is the gated one.
//
// With no password configured it returns uiAddr unchanged and starts nothing —
// the desktop default stays exactly what it was, one server on one port.
func gatedServer(ctx context.Context, uiAddr string, log logrus.FieldLogger) (string, error) {
	if !uiPasswordSet() {
		return uiAddr, nil
	}

	engineAddr, err := reserveLoopbackAddr()
	if err != nil {
		return "", fmt.Errorf("reserve a private UI port: %w", err)
	}
	gate, err := net.Listen("tcp", uiAddr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", uiAddr, err)
	}

	engine := &url.URL{Scheme: "http", Host: engineAddr}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(engine)
			// The credential belongs to this gate; the engine has no use for
			// it and it should travel no further than it must.
			r.Out.Header.Del("Authorization")
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			// Nearly always the engine not having bound yet — the gate's
			// listener is up first. A caller polling for readiness wants the
			// honest answer.
			log.Debugf("skydex-client: trading UI not reachable yet: %v", err)
			http.Error(w, "skydex-client: trading UI not ready", http.StatusBadGateway)
		},
	}

	srv := &http.Server{Handler: requireAuth(proxy), ReadHeaderTimeout: 5 * time.Second}
	go func() { //nolint
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()
	go func() { //nolint
		if err := srv.Serve(gate); err != nil && err != http.ErrServerClosed {
			log.Errorf("skydex-client: UI gate stopped: %v", err)
		}
	}()

	log.Infof("skydex-client: trading UI is password-gated on %s", uiAddr)
	return engineAddr, nil
}

// reserveLoopbackAddr picks a free loopback port by binding one and letting it
// go again.
//
// The window between the close here and the engine's own bind is a race in
// principle. It is a tolerable one: the kernel does not hand the same ephemeral
// port straight back, and losing the race is loud — the engine fails to bind,
// its Run returns, and the app reports stopping — rather than quietly serving
// somewhere unexpected.
func reserveLoopbackAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	return l.Addr().String(), l.Close()
}
