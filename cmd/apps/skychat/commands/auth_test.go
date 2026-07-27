// Package commands cmd/apps/skychat/commands/auth_test.go
//
// Unit coverage for the optional HTTP basic-auth gate: password-file
// loading (present / absent / malformed) and the requireAuth wrapper's
// three admit paths (no-auth passthrough, internal-token bypass, valid
// basic-auth) plus the two reject paths.
package commands

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// writePasswordFile writes a "<hex-salt>:<hex-hash>" file for password,
// matching auth.go's salt+SHA256 scheme, and returns its path.
func writePasswordFile(t *testing.T, password string, salt []byte) string {
	t.Helper()
	h := cipher.SumSHA256(append([]byte(password), salt...))
	line := hex.EncodeToString(salt) + ":" + hex.EncodeToString(h[:])
	path := filepath.Join(t.TempDir(), "skychat-auth")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	return path
}

// resetAuth clears global auth state on cleanup so tests don't leak
// state into each other.
func resetAuth(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = loadSkychatPassword("") //nolint:errcheck
		setSkychatInternalToken("")
	})
}

func okHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
}

func TestLoadSkychatPassword_PresentAbsent(t *testing.T) {
	resetAuth(t)

	// Empty path -> no auth.
	if err := loadSkychatPassword(""); err != nil {
		t.Fatalf("empty path: %v", err)
	}
	if authPasswordSet {
		t.Error("empty path should not set a password")
	}

	// Missing file -> no auth, no error.
	if err := loadSkychatPassword(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("missing file should be nil, got %v", err)
	}
	if authPasswordSet {
		t.Error("missing file should not set a password")
	}

	// Valid file -> set.
	p := writePasswordFile(t, "hunter2", []byte("saltysalt"))
	if err := loadSkychatPassword(p); err != nil {
		t.Fatalf("valid file: %v", err)
	}
	if !authPasswordSet {
		t.Fatal("valid file should set the password")
	}
}

func TestLoadSkychatPassword_Malformed(t *testing.T) {
	resetAuth(t)
	dir := t.TempDir()

	// No colon -> treated as no-auth (nil, not set).
	noColon := filepath.Join(dir, "nocolon")
	if err := os.WriteFile(noColon, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSkychatPassword(noColon); err != nil {
		t.Fatalf("malformed line should be nil, got %v", err)
	}
	if authPasswordSet {
		t.Error("malformed line should not set a password")
	}

	// Empty (whitespace-only) file -> no auth.
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSkychatPassword(empty); err != nil {
		t.Fatalf("empty file: %v", err)
	}
	if authPasswordSet {
		t.Error("empty file should not set a password")
	}

	// Wrong hash length -> not set (returns nil).
	shortHash := filepath.Join(dir, "shorthash")
	line := hex.EncodeToString([]byte("salt")) + ":" + hex.EncodeToString([]byte("tooshort"))
	if err := os.WriteFile(shortHash, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSkychatPassword(shortHash); err != nil {
		t.Fatalf("short hash should be nil, got %v", err)
	}
	if authPasswordSet {
		t.Error("short hash should not set a password")
	}

	// Bad hex salt -> error.
	badSalt := filepath.Join(dir, "badsalt")
	if err := os.WriteFile(badSalt, []byte("zzzz:"+hex.EncodeToString(make([]byte, 32))), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSkychatPassword(badSalt); err == nil {
		t.Error("bad hex salt should return an error")
	}
}

func TestRequireAuth_NoPasswordPassthrough(t *testing.T) {
	resetAuth(t)
	if err := loadSkychatPassword(""); err != nil { // ensure unset
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	requireAuth(okHandler()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-auth passthrough: code=%d want 200", rr.Code)
	}
}

func TestRequireAuth_GatesWhenSet(t *testing.T) {
	resetAuth(t)
	p := writePasswordFile(t, "s3cret", []byte("NaCl"))
	if err := loadSkychatPassword(p); err != nil {
		t.Fatal(err)
	}

	// No credentials -> 401 + WWW-Authenticate challenge.
	rr := httptest.NewRecorder()
	requireAuth(okHandler()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing creds: code=%d want 401", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 should carry a WWW-Authenticate header")
	}

	// Wrong password -> 401.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("user", "wrong")
	requireAuth(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: code=%d want 401", rr.Code)
	}

	// Correct password -> passthrough.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("user", "s3cret")
	requireAuth(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("correct password: code=%d want 200", rr.Code)
	}
}

func TestRequireAuth_InternalTokenBypass(t *testing.T) {
	resetAuth(t)
	p := writePasswordFile(t, "pw", []byte("salt"))
	if err := loadSkychatPassword(p); err != nil {
		t.Fatal(err)
	}
	setSkychatInternalToken("secret-token")

	// Matching internal token bypasses the password gate.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Skychat-Internal-Token", "secret-token")
	requireAuth(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("internal-token bypass: code=%d want 200", rr.Code)
	}

	// A wrong token still requires basic auth.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Skychat-Internal-Token", "nope")
	requireAuth(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: code=%d want 401", rr.Code)
	}
}

func TestRequireAuthFunc_WrapsSameGate(t *testing.T) {
	resetAuth(t)
	p := writePasswordFile(t, "pw", []byte("salt"))
	if err := loadSkychatPassword(p); err != nil {
		t.Fatal(err)
	}
	called := false
	h := requireAuthFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("requireAuthFunc should gate: called=%v code=%d", called, rr.Code)
	}
}
