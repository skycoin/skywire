// Package ui2 pkg/visor/ui2/routing.go c3-vis-core
package ui2

import (
	"cmp"
	"sort"
	"strconv"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// RoutingViews are the routing tab's sub views, after routing.component.html.
// The mux tree is the route visualizer page.
var RoutingViews = []string{"rules", "groups", "find", "calc", "policies"}

// Rule is one routing rule row.
type Rule struct {
	Key        string
	Type       string
	LocalPort  string
	RemotePK   string
	RemotePort string
	NextRID    string
	NextTP     string
	KeepAlive  string
}

// RulesOf turns the visor's rules into rows, by key.
func RulesOf(rules []routing.Rule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		s := r.Summary()
		if s == nil {
			continue
		}
		row := Rule{Key: strconv.FormatUint(uint64(s.KeyRouteID), 10), Type: s.Type.String(), KeepAlive: s.KeepAlive.String()}
		desc := func(d routing.RouteDescriptorFields) {
			row.LocalPort = strconv.Itoa(int(d.SrcPort))
			row.RemotePK = d.DstPK.Hex()
			row.RemotePort = strconv.Itoa(int(d.DstPort))
		}
		next := func(rid routing.RouteID, tp string) {
			row.NextRID = strconv.FormatUint(uint64(rid), 10)
			row.NextTP = tp
		}
		switch {
		case s.ConsumeFields != nil:
			desc(s.ConsumeFields.RouteDescriptor)
		case s.ForwardFields != nil:
			desc(s.ForwardFields.RouteDescriptor)
			next(s.ForwardFields.NextRID, s.ForwardFields.NextTID.String())
		case s.IntermediaryForwardFields != nil:
			next(s.IntermediaryForwardFields.NextRID, s.IntermediaryForwardFields.NextTID.String())
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.ParseUint(out[i].Key, 10, 64) //nolint:errcheck
		b, _ := strconv.ParseUint(out[j].Key, 10, 64) //nolint:errcheck
		return a < b
	})
	return out
}

// Group is one active route group row.
type Group struct {
	Initiator  bool
	Remote     string
	Ports      string
	FwdRID     string
	ConsumeRID string
	Hops       int
	NextTP     string
}

// GroupsOf turns the visor's route groups into rows.
func GroupsOf(gs []visorapi.RouteGroupInfo) []Group {
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		remote := g.Desc.SrcPK
		if g.Initiator {
			remote = g.Desc.DstPK
		}
		out = append(out, Group{
			Initiator:  g.Initiator,
			Remote:     remote.Hex(),
			Ports:      strconv.Itoa(int(g.Desc.SrcPort)) + ":" + strconv.Itoa(int(g.Desc.DstPort)),
			FwdRID:     strconv.FormatUint(uint64(g.FwdRuleID), 10),
			ConsumeRID: strconv.FormatUint(uint64(g.ConsumeRuleID), 10),
			Hops:       len(g.Hops),
			NextTP:     g.FwdNextTpID,
		})
	}
	return out
}

// Path is one route a route find or calculation returned: the visors it
// passes, ending at the destination.
type Path struct {
	Hops []string
	Dst  string
}

// PathQuery is a route find or calculation form and its answer.
type PathQuery struct {
	PK      string
	Calc    bool
	Dst     string
	MinHops string
	MaxHops string
	Count   string
	Paths   []Path
	Ran     bool
	Err     string
}

// Policies is the routing policies view.
type Policies struct {
	Default *visorapi.RoutingPolicyInfo
	Apps    []string
	PerApp  map[string]*visorapi.RoutingPolicyInfo
}

// PoliciesOf sorts the per-app policies by app name.
func PoliciesOf(s *visorapi.RoutingPoliciesSummary) Policies {
	p := Policies{PerApp: map[string]*visorapi.RoutingPolicyInfo{}}
	if s == nil {
		return p
	}
	p.Default = s.Default
	for name, info := range s.PerApp {
		if info != nil {
			p.Apps = append(p.Apps, name)
			p.PerApp[name] = info
		}
	}
	sort.Strings(p.Apps)
	return p
}

// Rules is one page of the routing rules view.
type Rules struct {
	PK    string
	Total int
	Rows  []Rule
	Pager Pager
	Err   string
}

func ruleKey(r Rule) uint64 {
	n, _ := strconv.ParseUint(r.Key, 10, 64) //nolint:errcheck
	return n
}

var ruleSorts = map[string]func(a, b Rule) int{
	"key":    func(a, b Rule) int { return cmp.Compare(ruleKey(a), ruleKey(b)) },
	"type":   func(a, b Rule) int { return cmp.Compare(a.Type, b.Type) },
	"remote": func(a, b Rule) int { return cmp.Compare(a.RemotePK, b.RemotePK) },
}

// SelectRules filters, sorts and pages rules for q.
func SelectRules(pk string, all []Rule, q ListQuery, errMsg string) Rules {
	rows := make([]Rule, 0, len(all))
	for _, x := range all {
		if q.Match(x.Key, x.Type, x.RemotePK, x.NextTP) {
			rows = append(rows, x)
		}
	}
	sortBy(rows, q, ruleSorts)
	r := Rules{PK: pk, Total: len(all), Err: errMsg}
	r.Rows, r.Pager = Paginate(rows, q, ListPageSize)
	return r
}

func (r Rules) url() string { return VisorURL(r.PK, "routing") + "/rules?list=1" }
