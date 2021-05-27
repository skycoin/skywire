//+build windows

package vpn

func (c *Client) setupSysPrivileges() (suid int, err error) {
	return 0, nil
}

func (c *Client) releaseSysPrivileges() {
	return
}
