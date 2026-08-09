package contacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	alice = "0311111111111111111111111111111111111111111111111111111111111111aa"
	bob   = "0322222222222222222222222222222222222222222222222222222222222222bb"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "contacts.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s
}

func TestNamesSurviveAReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contacts.json")

	first, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := first.Set(alice, "Alice"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// The whole point of moving off localStorage: the name outlives the
	// process that set it.
	second, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := second.Name(alice); got != "Alice" {
		t.Fatalf("Name(alice) = %q after reopen, want %q", got, "Alice")
	}
}

func TestSetNormalizesAndIsCaseInsensitive(t *testing.T) {
	s := openTemp(t)

	stored, err := OpenStore(filepath.Join(t.TempDir(), "x.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = stored

	if got, err := s.Set(alice, "  Alice   Anderson  "); err != nil || got != "Alice Anderson" {
		t.Fatalf("Set() = %q, %v; want %q", got, err, "Alice Anderson")
	}
	// A key is one identity however it is typed.
	if got := s.Name(strings.ToUpper(alice)); got != "Alice Anderson" {
		t.Fatalf("Name(upper) = %q, want the same contact", got)
	}
	// Over-long names are cut to the same cap the UI enforces, not rejected.
	long := strings.Repeat("x", 200)
	got, err := s.Set(bob, long)
	if err != nil {
		t.Fatalf("Set(long): %v", err)
	}
	if len([]rune(got)) > 40 {
		t.Fatalf("stored name is %d runes, want it capped at 40", len([]rune(got)))
	}
}

func TestEmptyNameForgetsTheContact(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Set(alice, "Alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(alice, "   "); err != nil {
		t.Fatalf("Set(blank): %v", err)
	}
	if got := s.Name(alice); got != "" {
		t.Fatalf("Name = %q after clearing, want empty", got)
	}
}

func TestSetRejectsAnythingThatIsNotAKey(t *testing.T) {
	s := openTemp(t)
	for _, pk := range []string{"", "alice", alice + "ff", strings.ToUpper("ZZ") + alice[2:]} {
		if _, err := s.Set(pk, "Alice"); err == nil {
			t.Fatalf("Set(%q) was accepted; a non-key can never match a real peer", pk)
		}
	}
}

// Merge is the migration path, so the rule that matters is that it cannot
// undo a rename the operator has made since.
func TestMergeFillsGapsAndNeverOverwrites(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Set(alice, "Alice (renamed)"); err != nil {
		t.Fatal(err)
	}

	added, err := s.Merge(map[string]string{
		alice:     "Alice (old)",
		bob:       "Bob",
		"garbage": "nobody",
		alice[:4]: "",
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if added != 1 {
		t.Fatalf("Merge added %d, want 1 (bob only)", added)
	}
	if got := s.Name(alice); got != "Alice (renamed)" {
		t.Fatalf("Name(alice) = %q — an import overwrote a rename", got)
	}
	if got := s.Name(bob); got != "Bob" {
		t.Fatalf("Name(bob) = %q, want Bob", got)
	}
}

func TestAllReturnsACopy(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Set(alice, "Alice"); err != nil {
		t.Fatal(err)
	}
	book := s.All()
	book[alice] = "tampered"
	if got := s.Name(alice); got != "Alice" {
		t.Fatalf("Name = %q — All() handed out the live map", got)
	}
}

// A truncated file is a real possibility (a crash mid-write), and starting
// blank would look exactly like every nickname having been discarded.
func TestUnparseableFileIsAnErrorNotAFreshStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contacts.json")
	if err := os.WriteFile(path, []byte(`{"broken":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("OpenStore accepted a corrupt file and would have started empty")
	}
}

func TestMissingFileIsAnEmptyBook(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "never-written.json"))
	if err != nil {
		t.Fatalf("OpenStore on a missing file: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("All() = %v, want empty", s.All())
	}
}

func TestNilStoreIsInert(t *testing.T) {
	var s *Store
	if got := s.Name(alice); got != "" {
		t.Fatalf("Name on a nil store = %q", got)
	}
	if got := s.All(); len(got) != 0 {
		t.Fatalf("All on a nil store = %v", got)
	}
	if _, err := s.Set(alice, "Alice"); err == nil {
		t.Fatal("Set on a nil store should report, not panic silently")
	}
}
