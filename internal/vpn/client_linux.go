//+build linux

package vpn

import (
	"fmt"
	"sync"

	"github.com/syndtr/gocapability/capability"
	"golang.org/x/sys/unix"
)

var (
	setupOnce sync.Once
)

func (c *Client) setupSysPrivileges() (suid int, err error) {
	setupOnce.Do(func() {
		var caps capability.Capabilities

		caps, err = capability.NewPid2(0)
		if err != nil {
			err = fmt.Errorf("failed to init capabilities: %w", err)
			return
		}

		err = caps.Load()
		if err != nil {
			err = fmt.Errorf("failed to load capabilities: %w", err)
			return
		}

		// set `CAP_NET_ADMIN` capability to needed caps sets.
		caps.Set(capability.CAPS|capability.BOUNDS|capability.AMBIENT, capability.CAP_NET_ADMIN)
		if err := caps.Apply(capability.CAPS | capability.BOUNDS | capability.AMBIENT); err != nil {
			err = fmt.Errorf("failed to apply capabilties: %w", err)
			return
		}

		// let child process keep caps sets from the parent, so we may do calls to
		// system utilities with these caps.
		if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0); err != nil {
			err = fmt.Errorf("failed to set PR_SET_KEEPCAPS: %w", err)
			return
		}
	})

	return 0, nil
}

func (c *Client) releaseSysPrivileges(_ int) {
	return
}
