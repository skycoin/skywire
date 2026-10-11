package visor

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
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
	if len(sum.Rows) != 3 {
		t.Fatalf("summary rows %v, want every service", sum.Rows)
	}
	for i, want := range [][2]string{{"transport-discovery", "running"}, {"route-finder", "starting"}, {"setup-node", "running"}} {
		if sum.Rows[i][0] != want[0] || sum.Rows[i][3] != want[1] {
			t.Errorf("row %d = %v, want %s %s", i, sum.Rows[i], want[0], want[1])
		}
	}
}
