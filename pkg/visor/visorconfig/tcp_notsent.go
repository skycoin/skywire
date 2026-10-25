// Package visorconfig pkg/visor/visorconfig/tcp_notsent.go c3-vis-core
package visorconfig

// UpdateTCPNotSentLowat sets transport.tcp_notsent_lowat and flushes. nil
// restores the default.
func (v1 *V1) UpdateTCPNotSentLowat(n *int) error {
	v1.mu.Lock()
	v1.Transport.TCPNotSentLowat = n
	v1.mu.Unlock()
	return v1.flush(v1)
}
