package proxystatus

import (
	"strings"
	"testing"
)

func TestRenderShowsTheAcceptCounters(t *testing.T) {
	var b strings.Builder
	writeAcceptSection(&b, Snapshot{})
	if b.Len() != 0 {
		t.Fatalf("no accept counters should render nothing, got %q", b.String())
	}
	writeAcceptSection(&b, Snapshot{Accept: &Accept{Accepted: 6, Opened: 5, OpenFailed: 1, MaxOpenMS: 120, LastOpenErr: "<boom>"}})
	out := b.String()
	for _, want := range []string{"6 accepted", "5 opened", "1 open failed", "0 no tunnel", "slowest open 120 ms", "&lt;boom&gt;"} {
		if !strings.Contains(out, want) {
			t.Errorf("accept line %q lacks %q", out, want)
		}
	}
}
