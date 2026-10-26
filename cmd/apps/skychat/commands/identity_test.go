package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityStatePath(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "skychat-contacts.json")
	if err := os.WriteFile(legacy, []byte(`{"a":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// No scope (standalone): the shared file, untouched.
	if got := identityStatePath(dir, "", "skychat-contacts.json"); got != legacy {
		t.Fatalf("unscoped: %s, want %s", got, legacy)
	}

	// The first identity adopts the unscoped file.
	first := identityStatePath(dir, "02aa", "skychat-contacts.json")
	want := filepath.Join(dir, "skychat-contacts-02aa.json")
	if first != want {
		t.Fatalf("scoped: %s, want %s", first, want)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy file still there after adoption")
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != `{"a":"b"}` { //nolint:gosec
		t.Fatalf("adopted content: %q %v", b, err)
	}

	// A second identity starts clean: nothing left to adopt.
	second := identityStatePath(dir, "03bb", "skychat-contacts.json")
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("second identity adopted something")
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("first identity's file was disturbed")
	}

	// Extensions stay readable.
	if got := scopedName("skychat-history.db", "02aa"); got != "skychat-history-02aa.db" {
		t.Fatalf("scopedName db: %s", got)
	}
	if got := scopedName("skychat-privacy.json", "02aa"); got != "skychat-privacy-02aa.json" {
		t.Fatalf("scopedName json: %s", got)
	}
}
