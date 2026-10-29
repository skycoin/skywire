// Package netutil pkg/netutil/policy.go c0-com-util
package netutil

import (
	"fmt"
	"net"
	"path"
	"strings"
	"sync/atomic"
)

// AddrPolicy decides which local interfaces and addresses the visor offers to
// peers. An entry is an interface name glob ("wlan*", "eth0") or an address
// range ("10.0.0.0/8", "192.168.1.5"). Deny wins over allow, and an empty
// allow list allows everything.
type AddrPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// IsZero reports whether the policy allows every address.
func (p AddrPolicy) IsZero() bool { return len(p.Allow) == 0 && len(p.Deny) == 0 }

// Validate rejects an entry that is neither a valid glob nor an address range.
func (p AddrPolicy) Validate() error {
	for _, list := range [][]string{p.Allow, p.Deny} {
		for _, e := range list {
			if err := ValidatePolicyEntry(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidatePolicyEntry checks one allow, deny or metered entry.
func ValidatePolicyEntry(e string) error {
	switch {
	case strings.TrimSpace(e) == "":
		return fmt.Errorf("empty network policy entry")
	case isAddrEntry(e):
		if parseAddrEntry(e) == nil {
			return fmt.Errorf("network policy entry %q is not a valid address or CIDR", e)
		}
	default:
		if _, err := path.Match(e, ""); err != nil {
			return fmt.Errorf("network policy entry %q is not a valid interface glob: %w", e, err)
		}
	}
	return nil
}

// isAddrEntry tells an address entry from a glob. Interface names hold no
// slash, colon or leading digit in practice, while every address entry does.
func isAddrEntry(e string) bool {
	return strings.ContainsAny(e, "/:") || net.ParseIP(e) != nil
}

func parseAddrEntry(e string) *net.IPNet {
	if _, n, err := net.ParseCIDR(e); err == nil {
		return n
	}
	ip := net.ParseIP(e)
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
}

// MatchPolicyEntry reports whether entry names the interface or holds the
// address. A nil ip or an empty name matches only the other kind.
func MatchPolicyEntry(entry, ifname string, ip net.IP) bool {
	if isAddrEntry(entry) {
		n := parseAddrEntry(entry)
		return n != nil && ip != nil && n.Contains(ip)
	}
	if ifname == "" {
		return false
	}
	ok, err := path.Match(entry, ifname)
	return err == nil && ok
}

func matchAny(list []string, ifname string, ip net.IP) bool {
	for _, e := range list {
		if MatchPolicyEntry(e, ifname, ip) {
			return true
		}
	}
	return false
}

// Permits reports whether the address ip on interface ifname may be offered.
func (p AddrPolicy) Permits(ifname string, ip net.IP) bool {
	if matchAny(p.Deny, ifname, ip) {
		return false
	}
	return len(p.Allow) == 0 || matchAny(p.Allow, ifname, ip)
}

// AllowsNamed reports whether an allow entry names ifname as an interface.
// Such an entry brings back an interface the virtual filter would drop.
func (p AddrPolicy) AllowsNamed(ifname string) bool {
	for _, e := range p.Allow {
		if !isAddrEntry(e) && MatchPolicyEntry(e, ifname, nil) {
			return true
		}
	}
	return false
}

// DeniesInterface reports whether a deny glob names ifname, so no address on
// it can be offered whatever the address is.
func (p AddrPolicy) DeniesInterface(ifname string) bool {
	for _, e := range p.Deny {
		if !isAddrEntry(e) && MatchPolicyEntry(e, ifname, nil) {
			return true
		}
	}
	return false
}

var advertisePolicy atomic.Pointer[AddrPolicy]

// SetAdvertisePolicy installs the policy LocalAddresses and the WebRTC ICE
// filter apply. It is process wide, like the interfaces it filters.
func SetAdvertisePolicy(p AddrPolicy) {
	cp := AddrPolicy{Allow: append([]string(nil), p.Allow...), Deny: append([]string(nil), p.Deny...)}
	advertisePolicy.Store(&cp)
}

// AdvertisePolicy returns the policy in force, the zero policy by default.
func AdvertisePolicy() AddrPolicy {
	if p := advertisePolicy.Load(); p != nil {
		return *p
	}
	return AddrPolicy{}
}
