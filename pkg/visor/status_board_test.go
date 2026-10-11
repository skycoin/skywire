package visor

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

type chartedService struct{ page *charts.Page }

func (chartedService) Run(context.Context) error { return nil }
func (chartedService) Embed(context.Context, services.Host) (http.Handler, error) {
	return http.NotFoundHandler(), nil
}
func (c chartedService) ChartsPage() *charts.Page { return c.page }

// The board lists every service and shows charts for the running ones
// that have a page.
func TestStatusBoard(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}}
	page := &charts.Page{Title: "Skywire transport discovery", Build: func(context.Context, charts.Range, time.Time) (charts.Content, error) {
		return charts.Content{}, nil
	}}
	v.embedded.svcs = []*embeddedService{
		{block: services.Block{Type: "transport-discovery", Name: "tpd"}, prefix: "/tpd", svc: chartedService{page}, running: true},
		{block: services.Block{Type: "route-finder", Name: "rf"}, prefix: "/rf", svc: chartedService{page}},
		{block: services.Block{Type: "setup-node", Name: "sn"}, standalone: true, ownPK: pk, running: true},
	}
	v.embedded.once.Do(func() {})

	secs := v.statusSections()
	if len(secs) != 1 || secs[0].Page != page {
		t.Fatalf("sections %+v, want the running transport discovery only", secs)
	}
	sum := v.statusSummary()
	want := [][]string{
		{"visor", pk.Hex(), buildinfo.Version(), "running", ""},
		{"transport-discovery (tpd)", pk.Hex(), buildinfo.Version(), "running", ""},
		{"route-finder (rf)", pk.Hex(), buildinfo.Version(), "starting", ""},
		{"setup-node (sn)", pk.Hex(), buildinfo.Version(), "running", ""},
	}
	if len(sum.Rows) != len(want) {
		t.Fatalf("summary rows %v, want the visor and every service", sum.Rows)
	}
	for i := range want {
		for j := range want[i] {
			if sum.Rows[i][j] != want[i][j] {
				t.Errorf("row %d = %v, want %v", i, sum.Rows[i], want[i])
				break
			}
		}
	}
}

func TestDmsgURLPK(t *testing.T) {
	for in, want := range map[string]string{
		"dmsg://02ab:80/tpd": "02ab",
		"dmsg://02ab":        "02ab",
		"dmsg://02ab/x":      "02ab",
		"":                   "",
		"http://02ab":        "",
	} {
		if got := dmsgURLPK(in); got != want {
			t.Errorf("dmsgURLPK(%q) = %q, want %q", in, got, want)
		}
	}
}

// A visor running a dmsg server lists it under its key, after the visor.
func TestStatusSummaryDmsgServer(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk},
		Dmsg: &dmsgc.DmsgConfig{Server: &dmsgc.DmsgServerConfig{Enabled: true}}}}
	v.embedded.once.Do(func() {})
	sum := v.statusSummary()
	if len(sum.Rows) != 2 || sum.Rows[1][0] != "dmsg server" || sum.Rows[1][1] != pk.Hex() || sum.Rows[1][3] != "starting" {
		t.Fatalf("summary rows %v, want the visor then its dmsg server", sum.Rows)
	}
}
