//+build darwin

package vpn

import (
	"fmt"
	"syscall"
)

func (c *Client) setupSysPrivileges() (suid int, err error) {
	c.sysPrivilegesMx.Lock()

	suid = syscall.Getuid()

	if err := Setuid(0); err != nil {
		return 0, fmt.Errorf("failed to setuid 0: %w", err)
	}

	return suid, nil
}

func (c *Client) releaseSysPrivileges(suid int) {
	c.sysPrivilegesMx.Unlock()

	if err := Setuid(suid); err != nil {
		c.log.WithError(err).Errorf("Failed to set uid %d", suid)
	}
}
