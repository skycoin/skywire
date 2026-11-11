//go:build linux

package call

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func listenSocket(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec
}

func TestFindUserPulseSocket(t *testing.T) {
	// Unix socket paths are limited to ~108 bytes, so keep the root short.
	run, err := os.MkdirTemp("", "ru")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(run) //nolint:errcheck
	seat := filepath.Join(run, "seat0")
	sock := func(uid string) string { return filepath.Join(run, uid, "pulse", "native") }

	if got := findUserPulseSocket(run, seat); got != "" {
		t.Fatalf("empty dir: got %q", got)
	}

	// A system uid and a plain file in place of a socket never count.
	listenSocket(t, sock("120"))
	if err := os.MkdirAll(filepath.Dir(sock("1001")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sock("1001"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findUserPulseSocket(run, seat); got != "" {
		t.Fatalf("no user socket: got %q", got)
	}

	listenSocket(t, sock("1002"))
	listenSocket(t, sock("1005"))
	if got := findUserPulseSocket(run, seat); got != sock("1002") {
		t.Fatalf("lowest uid: got %q", got)
	}

	if err := os.WriteFile(seat, []byte("# private\nACTIVE=c2\nACTIVE_UID=1005\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findUserPulseSocket(run, seat); got != sock("1005") {
		t.Fatalf("active seat user: got %q", got)
	}

	// An active user without a socket falls back to the scan.
	if err := os.WriteFile(seat, []byte("ACTIVE_UID=1009\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findUserPulseSocket(run, seat); got != sock("1002") {
		t.Fatalf("active user without socket: got %q", got)
	}
}

// Only root without a server of its own looks for a user's socket.
func TestRootPulseSocketLeavesOthersAlone(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := rootPulseSocket(); got != "" {
		t.Fatalf("with XDG_RUNTIME_DIR set: got %q", got)
	}
	if os.Geteuid() != 0 {
		t.Setenv("XDG_RUNTIME_DIR", "")
		if got := rootPulseSocket(); got != "" {
			t.Fatalf("non-root: got %q", got)
		}
	}
}
