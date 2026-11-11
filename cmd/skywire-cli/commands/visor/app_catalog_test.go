package clivisor

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestFormatAppCatalog(t *testing.T) {
	text := formatAppCatalog([]visorapi.CatalogEntry{
		{Name: "skysocks", Kind: "server", Description: "SOCKS5 proxy exit."},
		{Name: "skysocks-client", Kind: "client", Instances: []visorapi.CatalogInstance{
			{Name: "skysocks-client", Status: "stopped"},
			{Name: "skysocks-client-2", Status: "running"},
		}},
	})
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header and 2 rows, got:\n%s", text)
	}
	if f := strings.Fields(lines[1]); f[0] != "skysocks" || f[2] != "-" {
		t.Errorf("unconfigured row: %q", lines[1])
	}
	if !strings.Contains(lines[2], "skysocks-client:stopped,skysocks-client-2:running") {
		t.Errorf("instances row: %q", lines[2])
	}
}
