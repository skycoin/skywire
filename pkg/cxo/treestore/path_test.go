package treestore

import "testing"

func TestSplitPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{"", nil, false},
		{"a", []string{"a"}, false},
		{"a/b", []string{"a", "b"}, false},
		{"a/b/c", []string{"a", "b", "c"}, false},
		{"/a", nil, true},            // empty leading segment
		{"a/", []string{"a"}, false}, // single trailing slash tolerated for prefix-style use
		{"a//b", nil, true},          // empty middle segment
	}
	for _, c := range cases {
		got, err := SplitPath(c.in)
		if (err != nil) != c.err {
			t.Errorf("SplitPath(%q) err=%v, want err=%v", c.in, err, c.err)
			continue
		}
		if !c.err && !equalSlice(got, c.want) {
			t.Errorf("SplitPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestJoinPath(t *testing.T) {
	if got, err := JoinPath("a", "b", "c"); err != nil || got != "a/b/c" {
		t.Errorf("JoinPath: got %q err=%v", got, err)
	}
	if _, err := JoinPath("a", "", "c"); err == nil {
		t.Error("expected error for empty segment")
	}
	if _, err := JoinPath("a", "x/y"); err == nil {
		t.Error("expected error for slash in segment")
	}
}

func TestHasPrefix(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"tiers/dmsg/2026-04-27", "", true},
		{"tiers/dmsg/2026-04-27", "tiers", true},
		{"tiers/dmsg/2026-04-27", "tiers/dmsg", true},
		{"tiers/dmsg/2026-04-27", "tiers/dmsg/2026-04-27", true},
		{"tiers/dmsg2", "tiers/dmsg", false}, // segment-aware: not byte prefix
		{"tier", "tiers", false},
		{"", "", true},
		{"a", "a/b", false},
	}
	for _, c := range cases {
		if got := HasPrefix(c.path, c.prefix); got != c.want {
			t.Errorf("HasPrefix(%q, %q) = %v, want %v", c.path, c.prefix, got, c.want)
		}
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
