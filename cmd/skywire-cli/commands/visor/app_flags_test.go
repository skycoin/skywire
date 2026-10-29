package clivisor

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestFormatAppFlags(t *testing.T) {
	text := formatAppFlags(visorapi.AppFlags{App: "skysocks-client", Flags: []visorapi.AppFlag{
		{Name: "addr", Type: "string", Default: ":1080", Usage: "Client address to listen on"},
		{Name: "srv", Type: "string", Usage: "PubKey of the server", Value: "02ab", Set: true},
		{Name: "config", Shorthand: "c", Type: "bool", Default: "false", Set: true},
	}})
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header and 3 rows, got:\n%s", text)
	}
	if f := strings.Fields(lines[1]); f[0] != "--addr" || f[2] != ":1080" || f[3] != "-" {
		t.Errorf("default row: %q", lines[1])
	}
	if f := strings.Fields(lines[2]); f[2] != "-" || f[3] != "02ab" || f[4] != "*" {
		t.Errorf("set row: %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "-c, --config") || !strings.Contains(lines[3], "- *") {
		t.Errorf("shorthand row with empty value: %q", lines[3])
	}
}
