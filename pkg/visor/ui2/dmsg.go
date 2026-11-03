// Package ui2 pkg/visor/ui2/dmsg.go c3-vis-core
package ui2

import "github.com/skycoin/skywire/pkg/visor/visorapi"

// Dmsg is the dmsg tab: the visor's dmsg clients and their server sessions.
type Dmsg struct {
	PK      string
	Clients []*visorapi.DmsgClientSessionInfo
	Count   int // the main client's sessions, the form's starting value
	Err     string
	Fetched string
	// Result and Action are the last connect-all or sessions_count outcome.
	Result *visorapi.DmsgConnectAllResult
	Action string
}

// DmsgOf lists the clients a visor runs, main first.
func DmsgOf(s *visorapi.DmsgClientSessions) ([]*visorapi.DmsgClientSessionInfo, int) {
	if s == nil {
		return nil, 0
	}
	var out []*visorapi.DmsgClientSessionInfo
	for _, c := range []*visorapi.DmsgClientSessionInfo{s.Main, s.RouteSetup, s.TransportSetup} {
		if c != nil {
			out = append(out, c)
		}
	}
	n := 0
	if s.Main != nil {
		n = s.Main.Count
	}
	return out, n
}

// RoleKey names a dmsg client's role, as the Angular tab words it.
func RoleKey(role string) string {
	switch role {
	case "main", "route_setup", "transport_setup":
		return "ui2.dmsg.role-" + role
	}
	return role
}

// Protocol is how the client reached server pk, or "".
func Protocol(c *visorapi.DmsgClientSessionInfo, pk string) string {
	for _, s := range c.Sessions {
		if s.PK.Hex() == pk {
			return s.Protocol
		}
	}
	return ""
}
