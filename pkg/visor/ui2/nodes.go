// Package ui2 pkg/visor/ui2/nodes.go c3-vis-core
package ui2

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Node is one row of the visor list.
type Node struct {
	PK       string
	Hostname string
	Version  string
	Online   bool
	// State is the hypervisor's state for the visor, see alerts.State.
	State       string
	HV          bool
	PublicIP    string
	LocalIP     string
	Location    string
	Transports  int
	Apps        int
	AppNames    []string
	AppsRunning int
	Routes      int
	DmsgServer  string
	DmsgPing    string
	PingMS      int
	Reward      string
	LastSeen    string
	// Outdated is a version older than the newest one in the list.
	Outdated bool

	// ViaSub is a visor only a sub-hypervisor manages, so its pages do not
	// open here. Children are the visors of a sub-hypervisor, and SubErr
	// why they could not be listed.
	ViaSub   bool
	Children []Node
	SubErr   string
}

// NodeOf turns a visor summary into a row.
func NodeOf(s visorapi.Summary) Node {
	n := Node{Online: s.Online, State: s.State, HV: s.Hypervisor, Reward: s.RewardAddress, Routes: len(s.Routes)}
	if o := s.Overview; o != nil {
		n.PK = o.PubKey.Hex()
		n.Hostname = o.Hostname
		if o.BuildInfo != nil {
			n.Version = o.BuildInfo.Version
		}
		n.PublicIP, n.LocalIP = o.PublicIP, o.LocalIP
		var loc []string
		for _, p := range []string{o.CityName, o.RegionName, o.CountryCode} {
			if p != "" {
				loc = append(loc, p)
			}
		}
		n.Location = strings.Join(loc, ", ")
		n.Transports = len(o.Transports)
		n.Apps = len(o.Apps)
		for _, a := range o.Apps {
			if a != nil {
				n.AppNames = append(n.AppNames, a.Name)
			}
			if a != nil && a.Status == appserver.AppStatusRunning {
				n.AppsRunning++
			}
		}
		if n.Routes == 0 {
			n.Routes = o.RoutesCount
		}
	}
	if d := s.DmsgStats; d != nil {
		n.DmsgServer = d.ServerPK.Hex()
		n.DmsgPing = Millis(d.RoundTrip)
		n.PingMS = int(d.RoundTrip.Milliseconds())
	}
	if s.LastSeenAt != nil && !s.Online {
		n.LastSeen = s.LastSeenAt.Local().Format(time.DateTime)
	}
	return n
}

// Held reports a visor that is not online but not offline either: it is
// reconnecting, restarting or detached.
func (n Node) Held() bool {
	return !n.Online && (n.State == "reconnecting" || n.State == "restarting" || n.State == "detached")
}

// SortNodes puts the hypervisor first, then online visors, then by name.
func SortNodes(ns []Node) {
	sort.SliceStable(ns, func(i, j int) bool {
		a, b := ns[i], ns[j]
		if a.HV != b.HV {
			return a.HV
		}
		if a.Online != b.Online {
			return a.Online
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.PK < b.PK
	})
}

// Millis prints a duration as whole milliseconds, or "" for zero.
func Millis(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

// Name is the hostname, else an IP, as the Angular UI's default label.
func (n Node) Name() string {
	for _, s := range []string{n.Hostname, n.PublicIP, n.LocalIP} {
		if s != "" {
			return s
		}
	}
	return "-"
}

// ShortVersion is the release part of a version such as
// v1.3.104-76-gc08475bdb, which fits the list on one line.
func (n Node) ShortVersion() string {
	if i := strings.IndexAny(n.Version, "-+"); i > 0 {
		return n.Version[:i]
	}
	return n.Version
}

// SearchText is what the list's search box matches, in lower case.
func (n Node) SearchText() string {
	return strings.ToLower(strings.Join([]string{n.Hostname, n.PK, n.PublicIP, n.LocalIP, n.Location, n.Version}, " "))
}

// versionParts reads major, minor, patch and the commits since the tag from
// a version such as v1.3.104-76-gc08475bdb, as the Angular list does.
func versionParts(v string) ([4]int, bool) {
	var p [4]int
	v = strings.TrimPrefix(v, "v")
	end := strings.IndexFunc(v, func(c rune) bool { return c != '.' && (c < '0' || c > '9') })
	if end < 0 {
		end = len(v)
	}
	rel, rest := v[:end], strings.TrimPrefix(v[end:], "-")
	f := strings.Split(rel, ".")
	if len(f) != 3 {
		return p, false
	}
	for i, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil {
			return p, false
		}
		p[i] = n
	}
	if c, _, _ := strings.Cut(rest, "-"); c != "" {
		if n, err := strconv.Atoi(c); err == nil {
			p[3] = n
		}
	}
	return p, true
}

func compareVersions(a, b [4]int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

// MarkOutdated flags every visor, nested ones included, that runs an older
// version than the newest one in the list.
func MarkOutdated(ns []Node) {
	var newest [4]int
	var seen bool
	walk := func(f func(*Node)) {
		for i := range ns {
			f(&ns[i])
			for j := range ns[i].Children {
				f(&ns[i].Children[j])
			}
		}
	}
	walk(func(n *Node) {
		if v, ok := versionParts(n.Version); ok && (!seen || compareVersions(v, newest) > 0) {
			newest, seen = v, true
		}
	})
	walk(func(n *Node) {
		v, ok := versionParts(n.Version)
		n.Outdated = ok && compareVersions(v, newest) < 0
	})
}

// VersionKey sorts versions as text, oldest first, and unknown ones first.
func (n Node) VersionKey() string {
	v, ok := versionParts(n.Version)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%06d.%06d.%06d.%06d", v[0], v[1], v[2], v[3])
}
