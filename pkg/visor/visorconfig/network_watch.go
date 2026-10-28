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
