// Package visorapi pkg/visor/visorapi/network.go c3-vis-core
package visorapi

import "time"

// NetworkState is how the visor sees the network it is on, and how it last
// reacted to a change of network. It is the network section of StateSnapshot
// and what `skywire cli visor network show` prints.
type NetworkState struct {
	// PublicIP and PublicIPv6 are the addresses the visor declares to the
	// address resolver and service discovery.
	PublicIP   string `json:"public_ip,omitempty"`
	PublicIPv6 string `json:"public_ip_v6,omitempty"`
	// PublicIPSource is how PublicIP was learned, "dmsg" or "stun".
	PublicIPSource string    `json:"public_ip_source,omitempty"`
	PublicIPAt     time.Time `json:"public_ip_at,omitzero"`
	// NAT is the STUN classification and NATAt when it was made.
	NAT   string    `json:"nat,omitempty"`
	NATAt time.Time `json:"nat_at,omitzero"`
	// LocalAddresses is what the visor would advertise as its LAN addresses
	// now, after the virtual interface filter.
	LocalAddresses []string `json:"local_addresses"`
	// Allow and Deny are the network policy in force (transport.network),
	// and PolicyExcluded the addresses it removed, as "address (interface)".
	Allow          []string `json:"allow,omitempty"`
	Deny           []string `json:"deny,omitempty"`
	PolicyExcluded []string `json:"policy_excluded,omitempty"`
	// Metered says whether the network counts as metered, and
	// MeteredSource what said so: "manual", an app ("android"), or "rule".
	Metered       bool   `json:"metered"`
	MeteredSource string `json:"metered_source,omitempty"`
	// Mode is the mode in force since ModeSince, and MeteredMode the one
	// configured for a metered network.
	Mode              string    `json:"mode"`
	ModeSince         time.Time `json:"mode_since,omitzero"`
	MeteredMode       string    `json:"metered_mode"`
	MeteredInterfaces []string  `json:"metered_interfaces,omitempty"`
	// Advertised is the address list of the last bind of each transport type.
	Advertised map[string][]string `json:"advertised,omitempty"`
	// Moves counts network changes acted on since start. The last one is
	// described by LastMoveAt, LastMoveReason and LastMoveChange.
	Moves          uint64    `json:"moves"`
	LastMoveAt     time.Time `json:"last_move_at,omitzero"`
	LastMoveReason string    `json:"last_move_reason,omitempty"`
	LastMoveChange string    `json:"last_move_change,omitempty"`
	// Pending is true while a refresh is queued or running.
	Pending bool `json:"pending"`
	// WatchInterval and PublicIPCheckInterval are the live periods of the
	// address watcher and the public IP recheck, "off" when disabled.
	WatchInterval         string `json:"watch_interval"`
	PublicIPCheckInterval string `json:"public_ip_check_interval"`
}

// NetworkSettings is a change to the network policy, the argument of
// NetworkSet. A nil field keeps its value and an empty list clears it.
type NetworkSettings struct {
	Allow *[]string `json:"allow,omitempty"`
	Deny  *[]string `json:"deny,omitempty"`
	// Metered overrides the metered signal: "true", "false", or "auto" to
	// follow the apps and the interface rule again. Not saved.
	Metered *string `json:"metered,omitempty"`
	// MeteredMode is the mode used while metered, one of the Mode values.
	MeteredMode *string `json:"metered_mode,omitempty"`
	// MeteredInterfaces are the globs or ranges that mark the network as
	// metered while the route to the internet leaves through one.
	MeteredInterfaces *[]string `json:"metered_interfaces,omitempty"`
}

// The modes a visor runs in. A metered network switches to the configured
// MeteredMode, lean unless set otherwise, and back to normal when it ends.
const (
	// ModeNormal is the visor's usual behavior.
	ModeNormal = "normal"
	// ModeLean keeps dmsg and the transports it has, but stops autoconnect,
	// transit, dmsg relaying and new automatic inbound transports.
	ModeLean = "lean"
	// ModeDmsgOnly is lean, closes every direct transport, refuses new ones
	// and keeps at most two dmsg sessions.
	ModeDmsgOnly = "dmsg-only"
	// ModeOffline is dmsg-only that also stops dialing anything, dmsg
	// sessions and dmsg transports included.
	ModeOffline = "offline"
)

// Modes lists the modes in order of how much they cut.
var Modes = []string{ModeNormal, ModeLean, ModeDmsgOnly, ModeOffline}

// NetworkEvent is what an app reports about the network, the body of POST
// /api/network and the argument of the NetworkEvent RPC.
type NetworkEvent struct {
	// Event is "moved" when the network changed under the visor, or
	// "changed" when only its properties did.
	Event string `json:"event"`
	// Metered is the system's metered flag, nil when the app cannot tell.
	Metered *bool `json:"metered,omitempty"`
	// Interface is the interface the system now routes through, if known.
	Interface string `json:"ifname,omitempty"`
	// Source names the reporter, "android" when empty.
	Source string `json:"source,omitempty"`
	// MeteredText carries Metered over gob, which drops a pointer to false.
	// The RPC client fills it and the RPC server reads it.
	MeteredText string `json:"-"`
}
