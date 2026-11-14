// Package ui2 pkg/visor/ui2/bulk.go c3-vis-core
package ui2

import (
	"slices"
	"sort"
)

// The bulk actions of the visor list, as the Angular list's selection bar
// (node-list.component.ts and bulk-app-action.component.ts).
const (
	BulkRestart = "restart"
	BulkReward  = "reward"
	BulkAdd     = "add"
	BulkStart   = "start"
	BulkStop    = "stop"
)

// BulkAction reports whether a is one of the bulk actions.
func BulkAction(a string) bool {
	return slices.Contains([]string{BulkRestart, BulkReward, BulkAdd, BulkStart, BulkStop}, a)
}

// BulkRow is one selected visor and what the action does or did on it.
// Skip is the translation key of why it is left out.
type BulkRow struct {
	PK   string
	Name string
	Skip string
	Err  string
	Done bool
	// AddFirst is an add that must add the app before it can start it.
	AddFirst bool
}

// Bulk is the bulk action panel: the form before a run and the result
// after one.
type Bulk struct {
	Action     string
	Rows       []BulkRow
	Apps       []string
	App        string
	Address    string
	AcceptRisk bool
	StartAfter bool
	Ran        bool
	Err        string
}

// Todo is how many visors the action runs on.
func (b Bulk) Todo() int {
	n := 0
	for _, r := range b.Rows {
		if r.Skip == "" {
			n++
		}
	}
	return n
}

// Counts are the done, failed and skipped visors of a run.
func (b Bulk) Counts() (ok, failed, skipped int) {
	for _, r := range b.Rows {
		switch {
		case r.Skip != "":
			skipped++
		case r.Err != "":
			failed++
		case r.Done:
			ok++
		}
	}
	return ok, failed, skipped
}

// flatNodes is every row of the list, nested ones included.
func flatNodes(ns []Node) map[string]Node {
	m := map[string]Node{}
	for _, n := range ns {
		m[n.PK] = n
		for _, c := range n.Children {
			if _, ok := m[c.PK]; !ok {
				m[c.PK] = c
			}
		}
	}
	return m
}

// PlanBulk lists the selected visors with the reason each one is skipped,
// as the Angular dialogs do, and the apps the form offers: the catalog and
// the apps of the selected visors for an add, only the latter otherwise.
func PlanBulk(b Bulk, ns []Node, pks []string, catalog []string) Bulk {
	all := flatNodes(ns)
	apps := map[string]bool{}
	if b.Action == BulkAdd {
		for _, a := range catalog {
			apps[a] = true
		}
	}
	b.Rows = b.Rows[:0]
	for _, pk := range pks {
		n, ok := all[pk]
		r := BulkRow{PK: pk, Name: pk}
		if ok {
			r.Name = n.Name()
			for _, a := range n.AppNames {
				apps[a] = true
			}
		}
		has := ok && slices.Contains(n.AppNames, b.App)
		switch {
		case !ok || !n.Online:
			r.Skip = "nodes.bulk-apps.offline"
		case n.ViaSub:
			r.Skip = "nodes.bulk-apps.via-sub"
		case b.Action == BulkAdd && has && !b.StartAfter:
			r.Skip = "nodes.bulk-apps.already-added"
		case (b.Action == BulkStart || b.Action == BulkStop) && b.App != "" && !has:
			r.Skip = "nodes.bulk-apps.not-configured"
		}
		r.AddFirst = b.Action == BulkAdd && !has
		b.Rows = append(b.Rows, r)
	}
	b.Apps = b.Apps[:0]
	for a := range apps {
		b.Apps = append(b.Apps, a)
	}
	sort.Strings(b.Apps)
	return b
}

// BulkTitle is the translation key of the panel's heading.
func BulkTitle(action string) string {
	switch action {
	case BulkRestart:
		return "nodes.bulk.restart"
	case BulkReward:
		return "nodes.bulk.reward"
	}
	return "nodes.bulk-apps.title-" + action
}
