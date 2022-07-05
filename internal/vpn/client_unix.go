//go:build !windows
// +build !windows

package vpn

import "fmt"

func (c *Client) releaseSysPrivileges() { // nolint
	defer c.suidMu.Unlock()

	if err := releaseClientSysPrivileges(c.suid); err != nil {
		fmt.Printf("Failed to release system privileges: %v\n", err)
	}
}
