// Package visor pkg/visor/autoconnect_sites.go c3-vis-core
//
// Autoconnect's site limit. About 300 public IPs carry the network's ~900
// visors, and many sites run five to eight of them behind one home router.
// Dialing every reachable peer gave two such sites up to 64 transports
// between them, all through the same two uplinks and NATs: no extra path
// diversity, and thousands of UDP mappings for the router to keep alive.
//
// A site is the visors behind one public IP that share a LAN subnet, so
// unrelated visors behind one carrier-grade NAT stay apart. For a limited
// transport type, autoconnect keeps k links between two sites: of all the
// member pairs, the k with the lowest hash of the two keys. Both ends work
// that out from the same address-resolver data, so they agree on who dials
// whom without talking, and the links spread over each site's members.
// Siblings in one site are not limited; they reach the rest of the network
// through each other over the LAN.
package visor

import (
	"bytes"
	"hash/fnv"
	"net"
	"slices"
	"sort"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// siteMap groups visors into sites.
type siteMap struct {
	site    map[cipher.PubKey]int
	members [][]cipher.PubKey
}

// buildSites groups the visors in bindings, keyed by hex public key, into
// sites. A visor with no public IP or no LAN address is in no site.
func buildSites(bindings map[string]*arfeed.PeerBindings) siteMap {
	byIP := map[string][]cipher.PubKey{}
	nets := map[cipher.PubKey][]string{}
	for k, b := range bindings {
		var pk cipher.PubKey
		if pk.Set(k) != nil {
			continue
		}
		ip, lans := siteAddrs(b)
		if ip == "" || len(lans) == 0 {
			continue
		}
		byIP[ip] = append(byIP[ip], pk)
		nets[pk] = lans
	}
	sm := siteMap{site: map[cipher.PubKey]int{}}
	ips := make([]string, 0, len(byIP))
	for ip := range byIP {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	for _, ip := range ips {
		pks := byIP[ip]
		slices.SortFunc(pks, func(a, b cipher.PubKey) int { return bytes.Compare(a[:], b[:]) })
		// Visors behind one IP that share a subnet, directly or through another
		// member, are one site.
		parent := make([]int, len(pks))
		for i := range parent {
			parent[i] = i
		}
		find := func(i int) int {
			for parent[i] != i {
				parent[i] = parent[parent[i]]
				i = parent[i]
			}
			return i
		}
		owner := map[string]int{}
		for i, pk := range pks {
			for _, n := range nets[pk] {
				if j, ok := owner[n]; ok {
					parent[find(i)] = find(j)
				} else {
					owner[n] = i
				}
			}
		}
		groups := map[int]int{}
		for i, pk := range pks {
			r := find(i)
			id, ok := groups[r]
			if !ok {
				id = len(sm.members)
				groups[r] = id
				sm.members = append(sm.members, nil)
			}
			sm.members[id] = append(sm.members[id], pk)
			sm.site[pk] = id
		}
	}
	return sm
}

// siteAddrs returns a visor's public IP and the private /24 networks it
// advertises, from whichever of its bindings carries them.
func siteAddrs(b *arfeed.PeerBindings) (string, []string) {
	var ip string
	var lans []string
	for _, d := range []*addrresolver.VisorData{b.STCPR, b.SUDPH, b.QUIC, b.WT} {
		if d == nil {
			continue
		}
		if ip == "" {
			ip = publicIPOf(d)
		}
		for _, a := range d.Addresses {
			if n := privateNet24(a); n != "" && !slices.Contains(lans, n) {
				lans = append(lans, n)
			}
		}
	}
	return ip, lans
}

func publicIPOf(d *addrresolver.VisorData) string {
	for _, s := range []string{d.PublicIP, d.RemoteAddr} {
		host := s
		if h, _, err := net.SplitHostPort(s); err == nil {
			host = h
		}
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() {
			return ip.To4().String()
		}
	}
	return ""
}

// privateNet24 is the private IPv4 /24 that addr is in, or "".
func privateNet24(addr string) string {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host).To4()
	if ip == nil || !ip.IsPrivate() {
		return ""
	}
	return net.IPv4(ip[0], ip[1], ip[2], 0).String()
}

// pairScore orders a pair of visors the same way from either end.
func pairScore(a, b cipher.PubKey) uint64 {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	h := fnv.New64a()
	h.Write(a[:]) //nolint:errcheck,gosec // fnv never fails
	h.Write(b[:]) //nolint:errcheck,gosec
	return h.Sum64()
}

// siteAllows reports whether self may dial a transport of type t to remote
// under a limit of k links between their two sites. eligible says which
// members can take a transport of t at all, so links are not spent on pairs
// that cannot form one; nil counts every member.
func (sm siteMap) siteAllows(self, remote cipher.PubKey, k int, eligible func(cipher.PubKey) bool) bool {
	if k <= 0 {
		return true
	}
	ls, ok1 := sm.site[self]
	rs, ok2 := sm.site[remote]
	if !ok1 || !ok2 || ls == rs {
		return true
	}
	mine := slices.Clone(sm.members[ls])
	theirs := slices.Clone(sm.members[rs])
	if eligible != nil {
		mine = slices.DeleteFunc(mine, func(pk cipher.PubKey) bool { return pk != self && !eligible(pk) })
		theirs = slices.DeleteFunc(theirs, func(pk cipher.PubKey) bool { return pk != remote && !eligible(pk) })
	}
	if len(mine)*len(theirs) <= k {
		return true
	}
	want := pairScore(self, remote)
	below := 0
	for _, a := range mine {
		for _, b := range theirs {
			if s := pairScore(a, b); s < want || (s == want && pairKey(a, b) < pairKey(self, remote)) {
				below++
				if below >= k {
					return false
				}
			}
		}
	}
	return true
}

// pairKey breaks a tie between two equal scores, the same from either end.
func pairKey(a, b cipher.PubKey) string {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return string(a[:]) + string(b[:])
}

// filterBySite drops the targets the site limit keeps self from dialing
// over t this cycle, and returns them as skipped: their site reaches them,
// so they are not unreachable peers for a fallback type. It changes nothing
// while the bindings feed is unsynced.
func (a *autoconnector) filterBySite(v *Visor, t tptypes.Type, targets []cipher.PubKey, reach map[cipher.PubKey]*arfeed.Reach) (kept []cipher.PubKey, skipped map[cipher.PubKey]bool) {
	ac := v.conf.AutoconnectSettings()
	k := ac.Links()
	if k == 0 || !ac.SiteLimited(string(t)) || len(targets) == 0 {
		return targets, nil
	}
	bindings, ok := v.arBindingsAll()
	if !ok {
		return targets, nil
	}
	sm := buildSites(bindings)
	eligible := siteEligible(t, reach)
	self := v.conf.PK
	kept = targets[:0:0]
	for _, pk := range targets {
		if sm.siteAllows(self, pk, k, eligible) {
			kept = append(kept, pk)
			continue
		}
		if skipped == nil {
			skipped = map[cipher.PubKey]bool{}
		}
		skipped[pk] = true
	}
	if len(skipped) > 0 {
		a.log.WithField("type", string(t)).WithField("site_links", k).WithField("skipped", len(skipped)).
			Debug("Autoconnect: peers left to other members of their site")
	}
	return kept, skipped
}

// siteEligible reports which visors can take a transport of t, from the reach
// feed; every visor when the feed has nothing to say.
func siteEligible(t tptypes.Type, reach map[cipher.PubKey]*arfeed.Reach) func(cipher.PubKey) bool {
	rt, hasReach := reachName(t)
	return func(pk cipher.PubKey) bool {
		if !hasReach || reach == nil {
			return true
		}
		r := reach[pk]
		return r != nil && (slices.Contains(r.Bound, rt) || slices.Contains(r.Accepts, rt))
	}
}

// autoconnectDiag is the site limit as this visor applies it, for visor
// state. It reads only feeds that have already synced, so it never waits.
func (v *Visor) autoconnectDiag() *visorapi.DiagAutoconnect {
	if v.conf == nil {
		return nil
	}
	ac := v.conf.AutoconnectSettings()
	d := &visorapi.DiagAutoconnect{SiteLinks: ac.Links(), SiteTypes: ac.SiteTypes}
	bindings, ok := v.arBindingsAll()
	if !ok {
		return d
	}
	sm := buildSites(bindings)
	d.Visors, d.Sites = len(sm.site), len(sm.members)
	for _, m := range sm.members {
		if len(m) > 1 {
			d.MultiSites++
		}
	}
	self := v.conf.PK
	if s, ok := sm.site[self]; ok {
		d.Site = sm.members[s]
	}
	mgr := v.CXOSubMgr()
	if mgr == nil || mgr.LastSync(FeedARReach).IsZero() {
		return d
	}
	reach := map[cipher.PubKey]*arfeed.Reach{}
	mgr.Walk(FeedARReach, arfeed.ReachPathPrefix, func(_ string, body []byte) bool {
		if peers, err := arfeed.DecodeReachBucket(body); err == nil {
			for pk, r := range peers {
				reach[pk] = r
			}
		}
		return true
	})
	eligible := siteEligible(tptypes.SUDPH, reach)
	for pk := range reachPeers(reach, arfeed.TypeSUDPH) {
		if pk == self {
			continue
		}
		d.SudphPeers++
		if !ac.SiteLimited(string(tptypes.SUDPH)) || sm.siteAllows(self, pk, d.SiteLinks, eligible) {
			d.SudphAllowed++
		}
	}
	return d
}
