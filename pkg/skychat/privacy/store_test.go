package privacy

import (
	"path/filepath"
	"strings"
	"testing"
)

var (
	alice = "02" + strings.Repeat("a", 64)
	bob   = "03" + strings.Repeat("b", 64)
	carol = "02" + strings.Repeat("c", 64)
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s
}

func TestVerdicts(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "privacy.json"))
	if got := s.Verdict(alice, false); got != Allow {
		t.Fatalf("approval off: %v, want Allow", got)
	}
	if err := s.SetApproval(true, []string{bob}); err != nil {
		t.Fatal(err)
	}
	if got := s.Verdict(alice, false); got != Request {
		t.Errorf("stranger with approval on: %v, want Request", got)
	}
	if got := s.Verdict(alice, true); got != Allow {
		t.Errorf("a named contact: %v, want Allow", got)
	}
	if got := s.Verdict(strings.ToUpper(bob), false); got != Allow {
		t.Errorf("a peer chatting before approval was turned on: %v, want Allow", got)
	}
	if err := s.Accept(alice); err != nil {
		t.Fatal(err)
	}
	if got := s.Verdict(alice, false); got != Allow {
		t.Errorf("accepted: %v, want Allow", got)
	}
	if err := s.SetBlocked(alice, true); err != nil {
		t.Fatal(err)
	}
	if got := s.Verdict(alice, true); got != Reject {
		t.Errorf("blocked, even as a contact: %v, want Reject", got)
	}
	if err := s.SetBlocked(alice, false); err != nil {
		t.Fatal(err)
	}
	if got := s.Verdict(alice, false); got != Allow {
		t.Errorf("unblocked keeps the acceptance: %v, want Allow", got)
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "privacy.json")
	s := open(t, path)
	if err := s.SetApproval(true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(bob); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBlocked(carol, true); err != nil {
		t.Fatal(err)
	}
	r := open(t, path)
	if !r.Approval() || !r.Accepted(bob) || !r.Blocked(carol) {
		t.Fatalf("reopened: approval=%v accepted(bob)=%v blocked(carol)=%v",
			r.Approval(), r.Accepted(bob), r.Blocked(carol))
	}
	if got := r.BlockedList(); len(got) != 1 || got[0] != carol {
		t.Errorf("BlockedList = %v", got)
	}
}

func TestRejectsMalformedKeys(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "privacy.json"))
	if err := s.SetBlocked("not-a-key", true); err == nil {
		t.Error("blocking a malformed key succeeded")
	}
	var nilStore *Store
	if got := nilStore.Verdict(alice, false); got != Allow {
		t.Errorf("nil store: %v, want Allow", got)
	}
}
