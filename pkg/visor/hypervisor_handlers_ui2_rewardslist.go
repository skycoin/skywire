// Package visor pkg/visor/hypervisor_handlers_ui2_rewardslist.go c3-vis-core
package visor

import (
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The visor list's rewards view: each managed visor's last seven days of
// rewards from the reward system, fetched in parallel as the Angular view
// does.

func (hv *Hypervisor) ui2RewardsListRoutes(r *httputil.Router) {
	r.Get("/rewards", hv.ui2RewardsList())
}

func (hv *Hypervisor) ui2RewardsList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
		l := ui2.RewardsList{Dates: ui2.LastDays(time.Now(), 7)}
		keep := map[string]bool{}
		for _, d := range l.Dates {
			keep[d] = true
		}
		for _, s := range hv.collectLocalVisorSummaries() {
			l.Rows = append(l.Rows, ui2.RewardRow{Node: ui2.NodeOf(s), Daily: map[string]ui2.RewardDay{}})
		}
		var wg sync.WaitGroup
		for i := range l.Rows {
			wg.Add(1)
			go func(row *ui2.RewardRow) {
				defer wg.Done()
				body, _, err := hv.rewardSystemGet("skycoin-rewards/visor/" + row.PK + "?days=7")
				var days []ui2.RewardDay
				if err == nil {
					days, _, err = ui2.ParseRewardHistory(body)
				}
				if err != nil {
					row.Err = err.Error()
					return
				}
				for _, d := range days {
					if keep[d.Date] {
						row.Daily[d.Date] = d
						row.Total += d.Amount
					}
				}
			}(&l.Rows[i])
		}
		wg.Wait()
		ns := make([]ui2.Node, len(l.Rows))
		idx := map[string]ui2.RewardRow{}
		for i, row := range l.Rows {
			ns[i], idx[row.PK] = row.Node, row
		}
		ui2.SortNodes(ns)
		for i, n := range ns {
			l.Rows[i] = idx[n.PK]
		}
		hv.render(w, r, ui2.RewardsListPage(l))
	}
}
