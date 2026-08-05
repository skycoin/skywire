package commands

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// writePasswordFile writes a credential file in the on-disk format the gate
// reads: "<hex salt>:<hex sha256(password || salt)>".
func writePasswordFile(t *testing.T, password string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	hash := cipher.SumSHA256(append([]byte(password), salt...))
	path := filepath.Join(t.TempDir(), "skydex-password")
	record := hex.EncodeToString(salt) + ":" + hex.EncodeToString(hash[:])
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	return path
}

// resetAuth clears the package-level gate state between cases.
func resetAuth(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		authMu.Lock()
		authPasswordSet = false
		authPasswordSalt = nil
		authPasswordHash = cipher.SHA256{}
		authMu.Unlock()
	})
}

func TestLoadUIPassword(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	malformed := filepath.Join(t.TempDir(), "malformed")
	if err := os.WriteFile(malformed, []byte("not-a-record"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := []struct {
		name string
		path string
		set  bool
	}{
		{"unset path is ungated", "", false},
		{"missing file is ungated", filepath.Join(t.TempDir(), "absent"), false},
		{"empty file is ungated", empty, false},
		{"malformed record is ungated", malformed, false},
		{"valid record gates", writePasswordFile(t, "hunter2"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetAuth(t)
			if err := loadUIPassword(tc.path); err != nil {
				t.Fatalf("loadUIPassword: %v", err)
			}
			if got := uiPasswordSet(); got != tc.set {
				t.Fatalf("uiPasswordSet = %v, want %v", got, tc.set)
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	t.Run("ungated passes everything through", func(t *testing.T) {
		resetAuth(t)
		if err := loadUIPassword(""); err != nil {
			t.Fatalf("loadUIPassword: %v", err)
		}
		if code := serveOnce(t, requireAuth(ok), ""); code != http.StatusTeapot {
			t.Fatalf("status = %d, want %d", code, http.StatusTeapot)
		}
	})

	t.Run("gated", func(t *testing.T) {
		resetAuth(t)
		if err := loadUIPassword(writePasswordFile(t, "hunter2")); err != nil {
			t.Fatalf("loadUIPassword: %v", err)
		}
		if code := serveOnce(t, requireAuth(ok), ""); code != http.StatusUnauthorized {
			t.Fatalf("no credential: status = %d, want 401", code)
		}
		if code := serveOnce(t, requireAuth(ok), "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("wrong credential: status = %d, want 401", code)
		}
		if code := serveOnce(t, requireAuth(ok), "hunter2"); code != http.StatusTeapot {
			t.Fatalf("right credential: status = %d, want %d", code, http.StatusTeapot)
		}
	})
}

// TestGatedServerMovesTheEngine is the whole point of the gate: the address
// anyone can name carries the password, and the engine is somewhere else.
func TestGatedServerMovesTheEngine(t *testing.T) {
	resetAuth(t)
	if err := loadUIPassword(writePasswordFile(t, "hunter2")); err != nil {
		t.Fatalf("loadUIPassword: %v", err)
	}

	uiAddr := freeAddr(t)
	engineAddr, err := gatedServer(t.Context(), uiAddr, logging.MustGetLogger("test"))
	if err != nil {
		t.Fatalf("gatedServer: %v", err)
	}
	if engineAddr == uiAddr {
		t.Fatal("engine was left on the configured address; the gate has nothing to guard")
	}

	// Stand in for the engine on the address the gate expects it.
	engine := &http.Server{ //nolint:gosec
		Addr:              engineAddr,
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.URL.Path) }),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = engine.ListenAndServe() }() //nolint:errcheck
	defer engine.Close()                        //nolint:errcheck

	base := "http://" + uiAddr + "/api/status"
	if code, _ := get(t, base, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: status = %d, want 401", code)
	}
	code, body := get(t, base, "hunter2")
	if code != http.StatusOK {
		t.Fatalf("authenticated request: status = %d, want 200", code)
	}
	if body != "/api/status" {
		t.Fatalf("proxied path = %q, want %q", body, "/api/status")
	}
}

// TestGatedServerUngatedIsUntouched: with no password the desktop shape must
// not change — one server, on the address that was configured.
func TestGatedServerUngatedIsUntouched(t *testing.T) {
	resetAuth(t)
	if err := loadUIPassword(""); err != nil {
		t.Fatalf("loadUIPassword: %v", err)
	}
	addr, err := gatedServer(context.Background(), ":8051", logging.MustGetLogger("test"))
	if err != nil {
		t.Fatalf("gatedServer: %v", err)
	}
	if addr != ":8051" {
		t.Fatalf("engine address = %q, want the configured %q", addr, ":8051")
	}
}

// --- helpers ---

func serveOnce(t *testing.T, h http.Handler, password string) int {
	t.Helper()
	srv := &http.Server{ //nolint:gosec
		Addr:              freeAddr(t),
		Handler:           h,
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.ListenAndServe() }() //nolint:errcheck
	defer srv.Close()                        //nolint:errcheck
	code, _ := get(t, "http://"+srv.Addr+"/", password)
	return code
}

func get(t *testing.T, url, password string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if password != "" {
		req.SetBasicAuth("skywire", password)
	}
	// The server may still be binding when the first attempt goes out.
	var last error
	for range 20 {
		resp, err := http.DefaultClient.Do(req) //nolint:bodyclose
		if err != nil {
			last = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		defer resp.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	t.Fatalf("GET %s never answered: %v", url, last)
	return 0, ""
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}
