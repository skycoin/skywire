// Package clivisor cmd/skywire-cli/commands/visor/state_test.go
package clivisor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/visor"
)

func TestParseSelect(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"mux", []string{"mux"}},
		{"mux,health", []string{"mux", "health"}},
		{" mux , health ,", []string{"mux", "health"}},
		{",,", nil},
	}
	for _, c := range cases {
		got := parseSelect(c.in)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("parseSelect(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestWriteNDJSON_Whole: no --jq → the whole snapshot on ONE line ending in \n,
// and it parses back as JSON (a clean NDJSON record).
func TestWriteNDJSON_Whole(t *testing.T) {
	snap := &visor.StateSnapshot{RouteGroups: 3}
	var buf bytes.Buffer
	if err := writeNDJSON(&buf, snap, ""); err != nil {
		t.Fatalf("writeNDJSON: %v", err)
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want exactly one trailing newline, got %q", out)
	}
	if strings.Contains(strings.TrimSpace(out), "\n") {
		t.Fatalf("record must be a single line, got %q", out)
	}
	var back visor.StateSnapshot
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("NDJSON line does not parse: %v", err)
	}
	if back.RouteGroups != 3 {
		t.Errorf("round-trip route_groups = %d, want 3", back.RouteGroups)
	}
}

// TestWriteNDJSON_JQ: --jq projects each tick to a compact line (here a scalar).
func TestWriteNDJSON_JQ(t *testing.T) {
	snap := &visor.StateSnapshot{RouteGroups: 7}
	var buf bytes.Buffer
	if err := writeNDJSON(&buf, snap, ".route_groups"); err != nil {
		t.Fatalf("writeNDJSON: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "7" {
		t.Fatalf("jq NDJSON = %q, want \"7\"", got)
	}
}

// TestStreamTicks: emits immediately, keeps ticking, and stops promptly when the
// context is canceled (the Ctrl-C path).
func TestStreamTicks(t *testing.T) {
	var n atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		streamTicks(ctx, 5*time.Millisecond, func() { n.Add(1) })
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamTicks did not return after cancel")
	}
	if got := n.Load(); got < 2 {
		t.Fatalf("expected multiple ticks (immediate + interval), got %d", got)
	}
}
