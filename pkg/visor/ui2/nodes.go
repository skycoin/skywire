// Package ui2 pkg/visor/ui2/nodes.go c3-vis-core
package ui2

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Node is one row of the visor list.
type Node struct {
	PK          string
	Hostname    string
	Version     string
	Online      bool
	IsHV        bool
	PublicIP    string
	LocalIP     string
	Location    string
	Transports  int
	Apps        int
	AppsRunning int
	Routes      int
	DmsgServer  string
	DmsgPing    string
	Reward      string
	LastSeen    string
}

// NodeOf turns a visor summary into a row.
func NodeOf(s visorapi.Summary) Node {
	n := Node{Online: s.Online, IsHV: s.IsHypervisor, Reward: s.RewardAddress, Routes: len(s.Routes)}
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
	}
	if s.LastSeenAt != nil && !s.Online {
		n.LastSeen = s.LastSeenAt.Local().Format(time.DateTime)
	}
	return n
}

// SortNodes puts the hypervisor first, then online visors, then by name.
func SortNodes(ns []Node) {
	sort.SliceStable(ns, func(i, j int) bool {
		a, b := ns[i], ns[j]
		if a.IsHV != b.IsHV {
			return a.IsHV
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
