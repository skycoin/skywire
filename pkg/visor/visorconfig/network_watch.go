// Package visorconfig pkg/visor/visorconfig/network_watch.go c3-vis-core
package visorconfig

// UpdateNetworkWatch sets the network watcher periods and saves the config.
// A nil value leaves that period unchanged.
func (v1 *V1) UpdateNetworkWatch(watch, ipCheck *Duration) error {
	v1.mu.Lock()
	if watch != nil {
		v1.Transport.NetworkWatchInterval = *watch
	}
	if ipCheck != nil {
		v1.Transport.PublicIPCheckInterval = *ipCheck
	}
	v1.mu.Unlock()
	return v1.flush(v1)
}

// NetworkPolicy is transport.network: which interfaces and addresses the
// visor offers to peers. Entries are interface name globs or address ranges,
// deny wins, and an empty allow list allows everything.
type NetworkPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// UpdateNetworkPolicy sets transport.network and saves the config. A policy
// that allows everything removes the block.
func (v1 *V1) UpdateNetworkPolicy(p NetworkPolicy) error {
	v1.mu.Lock()
	if len(p.Allow) == 0 && len(p.Deny) == 0 {
		v1.Transport.Network = nil
	} else {
		v1.Transport.Network = &p
	}
	v1.mu.Unlock()
	return v1.flush(v1)
}
