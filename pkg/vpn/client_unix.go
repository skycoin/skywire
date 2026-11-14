//go:build !windows

// Package vpn pkg/vpn/client_unix.go c3-app-vpn
package vpn

func (c *Client) releaseSysPrivileges() { // nolint
	defer c.suidMu.Unlock()

	if err := releaseClientSysPrivileges(c.suid); err != nil {
		c.logger().WithError(err).Warn("Failed to release system privileges")
	}
}
