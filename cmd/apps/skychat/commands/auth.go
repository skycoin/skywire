// Package commands cmd/apps/skychat/commands/auth.go
//
// HTTP basic-auth gate for skychat. Off by default; on when a
// password file is configured and non-empty.
//
// File format mirrors the hypervisor's user-store hashing scheme
// (salt + SHA256, see pkg/visor/usermanager/user.go) so the same
// password-set flow works the same way for both surfaces. The
// file holds a single line of "<hex-salt>:<hex-hash>". The
// password-file path is managed via the hypervisor UI; the visor
// writes / removes it on SetSkychatPassword / ClearSkychatPassword.
//
// The hypervisor reverse-proxy at /api/visors/<pk>/skychat/* runs
// in-process on the visor and bypasses this gate via a startup-
// random "internal token" in the X-Skychat-Internal-Token header,
// so authenticated hvui sessions don't have to re-authenticate to
// skychat. The standalone :8001 surface stays password-gated.
package commands

import (
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	authMu            sync.RWMutex
	authPasswordSalt  []byte
	authPasswordHash  cipher.SHA256
	authPasswordSet   bool
	authInternalToken string
)

// loadSkychatPassword reads "<hex-salt>:<hex-hash>" from path.
// Empty file or missing path → no auth. Errors other than ENOENT
// are surfaced; the caller should log and continue with auth
// disabled rather than blocking startup on a transient FS hiccup.
func loadSkychatPassword(path string) error {
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

// setSkychatInternalToken records the per-startup token the
// hypervisor proxy uses to bypass the password gate. Empty disables
// the bypass.
func setSkychatInternalToken(t string) {
	authMu.Lock()
	defer authMu.Unlock()
	authInternalToken = strings.TrimSpace(t)
}

// requireAuth wraps a handler with HTTP basic auth gating. If no
// password is configured, the wrapped handler runs unchanged. If a
// password is configured, the request must either present basic
// auth that bcrypt-verifies against the stored hash, or carry a
// matching X-Skychat-Internal-Token header (from the visor's
// in-process proxy).
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.RLock()
		set := authPasswordSet
		salt := authPasswordSalt
		hash := authPasswordHash
		token := authInternalToken
		authMu.RUnlock()

		if !set {
			next.ServeHTTP(w, r)
			return
		}

		if token != "" && r.Header.Get("X-Skychat-Internal-Token") == token {
			next.ServeHTTP(w, r)
			return
		}

		_, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="skychat"`)
			http.Error(w, "skychat: authentication required", http.StatusUnauthorized)
			return
		}
		got := cipher.SumSHA256(append([]byte(password), salt...))
		if got != hash {
			w.Header().Set("WWW-Authenticate", `Basic realm="skychat"`)
			http.Error(w, "skychat: invalid credentials", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuthFunc is the http.HandlerFunc-shaped equivalent for
// HandleFunc-style registrations.
func requireAuthFunc(h http.HandlerFunc) http.HandlerFunc {
	wrapped := requireAuth(h)
	return func(w http.ResponseWriter, r *http.Request) {
		wrapped.ServeHTTP(w, r)
	}
}
