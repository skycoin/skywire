// Package visor pkg/visor/status_board.go c3-vis-core
package visor

import (
	"fmt"
	"strings"

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

// statusSummary lists the visor, the dmsg server it runs and every embedded
// service, each by the public key it answers on.
func (v *Visor) statusSummary() *charts.Table {
	t := &charts.Table{Title: "Services", Head: []string{"Service", "Public key", "State", "Store"}}
	if v.conf != nil {
		t.Rows = append(t.Rows, []string{"visor", v.conf.PK.Hex(), "running", ""})
	}
	if srv := v.RolesSnapshot().DmsgServer; srv.Enabled || srv.Running {
		state := "starting"
		if srv.Running {
			state = fmt.Sprintf("running, %d sessions, %d peer servers", srv.SessionCount, srv.PeerCount)
		}
		pk := ""
		if !srv.PK.Null() {
			pk = srv.PK.Hex()
		}
		t.Rows = append(t.Rows, []string{"dmsg server", pk, state, ""})
	}
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
		name := st.Type
		if st.Name != "" && st.Name != st.Type {
			name += " (" + st.Name + ")"
		}
		addr := dmsgURLPK(st.URL)
		if addr == "" {
			addr = st.PlainHTTP
		}
		t.Rows = append(t.Rows, []string{name, addr, state, st.State.Store})
	}
	return t
}

// dmsgURLPK is the public key in a dmsg://<pk>[:port][/path] URL.
func dmsgURLPK(u string) string {
	pk, ok := strings.CutPrefix(u, "dmsg://")
	if !ok {
		return ""
	}
	if i := strings.IndexAny(pk, ":/"); i >= 0 {
		pk = pk[:i]
	}
	return pk
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
