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
