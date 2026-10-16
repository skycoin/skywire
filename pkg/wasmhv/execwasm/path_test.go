// Package execwasm pkg/wasmhv/execwasm/path_test.go
package execwasm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathIn(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	if got := pathIn(dir, cache); got != filepath.Join(dir, ModuleName) {
		t.Fatalf("a writable binary directory keeps the module: %s", got)
	}

	ro := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(ro, 0o555); err != nil { //nolint:gosec // read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) }) //nolint:errcheck,gosec // so TempDir can remove it
	if writable(ro) {
		t.Skip("running as a user who can write a 0555 directory")
	}
	if got, want := pathIn(ro, filepath.Join(cache, "skywire")), filepath.Join(cache, "skywire", ModuleName); got != want {
		t.Fatalf("an unwritable binary directory uses the cache: got %s, want %s", got, want)
	}
	// A service with no $HOME has no user cache, but its unit's directory works.
	state := t.TempDir()
	if got, want := pathIn(ro, filepath.Join(ro, "cache"), state), filepath.Join(state, ModuleName); got != want {
		t.Fatalf("an unwritable first cache falls through to the next: got %s, want %s", got, want)
	}
	if got := pathIn(ro); got != filepath.Join(ro, ModuleName) {
		t.Fatalf("with no cache directory the binary's is kept: %s", got)
	}

	if err := os.Chmod(ro, 0o755); err != nil { //nolint:gosec // restoring it for the next write
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, ModuleName), []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o555); err != nil { //nolint:gosec // read-only on purpose
		t.Fatal(err)
	}
	if got := pathIn(ro, cache); got != filepath.Join(ro, ModuleName) {
		t.Fatalf("a module already beside the binary is used: %s", got)
	}
}
