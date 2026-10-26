// Package visorconfig pkg/visor/visorconfig/autoconnect.go c3-vis-core
package visorconfig

import "slices"

// DefaultAutoconnectSiteLinks is how many transports of each site type
// autoconnect keeps between two sites when SiteLinks is unset.
const DefaultAutoconnectSiteLinks = 2

// Autoconnect limits the transports public autoconnect dials. Each limit
// counts only the automatic transports this visor dialed itself, and
// reaching one stops new dials without closing any transport.
type Autoconnect struct {
	// SiteLinks is how many transports of SiteTypes autoconnect keeps between
	// two sites. A site is the visors behind one public IP that share a LAN
	// subnet. Unset means DefaultAutoconnectSiteLinks; 0 turns the limit off.
	SiteLinks *int `json:"site_links,omitempty"`
	// SiteTypes are the transport types SiteLinks applies to; empty means sudph.
	SiteTypes []string `json:"site_types,omitempty"`
	// MaxPerType caps the transports of a type autoconnect dials. A type left
	// out keeps its built-in cap; 0 means no cap.
	MaxPerType map[string]int `json:"max_per_type,omitempty"`
	// MaxTotal caps the transports autoconnect dials of all types; 0 means no cap.
	MaxTotal int `json:"max_total,omitempty"`
}

// Links is SiteLinks with its default applied.
func (a Autoconnect) Links() int {
	if a.SiteLinks == nil {
		return DefaultAutoconnectSiteLinks
	}
	return max(*a.SiteLinks, 0)
}

// SiteLimited reports whether the site limit applies to transport type t.
func (a Autoconnect) SiteLimited(t string) bool {
	if len(a.SiteTypes) == 0 {
		return t == "sudph"
	}
	return slices.Contains(a.SiteTypes, t)
}

// Cap is the dial cap for transport type t: MaxPerType's entry when set,
// otherwise builtin. 0 means no cap.
func (a Autoconnect) Cap(t string, builtin int) int {
	if c, ok := a.MaxPerType[t]; ok {
		return max(c, 0)
	}
	return builtin
}

// AutoconnectSettings returns a copy of the autoconnect limits.
func (v1 *V1) AutoconnectSettings() Autoconnect {
	v1.mu.RLock()
	defer v1.mu.RUnlock()
	a := v1.Transport.Autoconnect
	a.SiteTypes = slices.Clone(a.SiteTypes)
	if a.MaxPerType != nil {
		m := make(map[string]int, len(a.MaxPerType))
		for k, n := range a.MaxPerType {
			m[k] = n
		}
		a.MaxPerType = m
	}
	return a
}

// UpdateAutoconnect applies set to the autoconnect limits and flushes.
func (v1 *V1) UpdateAutoconnect(set func(*Autoconnect)) error {
	v1.mu.Lock()
	set(&v1.Transport.Autoconnect)
	v1.mu.Unlock()
	return v1.flush(v1)
}
