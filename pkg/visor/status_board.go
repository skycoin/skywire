// Package visor pkg/visor/status_board.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/services"
)

// statusBoard is the status page of the deployment services the visor
// embeds: one table of every service, then the charts of each that has them.
func (v *Visor) statusBoard() *charts.Board {
	v.embedded.boardOnce.Do(func() {
		v.embedded.board = &charts.Board{
			Title:    "Skywire deployment services",
			About:    v.conf.PK.Hex(),
			Summary:  v.statusSummary,
			Sections: v.statusSections,
			Log:      v.MasterLogger().PackageLogger("status_page"),
		}
	})
	return v.embedded.board
}

func (v *Visor) statusSummary() *charts.Table {
	t := &charts.Table{Title: "Services", Head: []string{"Service", "Name", "Address", "State", "Store"}}
	for _, st := range v.embeddedServiceStates() {
		state := "running"
		switch {
		case st.Error != "":
			state = "error: " + st.Error
		case st.Stopped:
			state = "stopped"
		case !st.Running:
			state = "starting"
		}
		t.Rows = append(t.Rows, []string{st.Type, st.Name, st.URL, state, st.State.Store})
	}
	return t
}

func (v *Visor) statusSections() []charts.Section {
	var out []charts.Section
	for _, es := range v.embeddedServices() {
		var svc services.Service = es.svc
		es.mu.Lock()
		running := es.running
		if es.standalone {
			svc = es.current
		}
		es.mu.Unlock()
		p, ok := svc.(services.ChartsPager)
		if !ok || !running {
			continue
		}
		if page := p.ChartsPage(); page != nil {
			out = append(out, charts.Section{Page: page})
		}
	}
	return out
}
