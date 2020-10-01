//+build linux

package vpn

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/syndtr/gocapability/capability"
	"golang.org/x/sys/unix"
)

// SetupTUN sets the allocated TUN interface up, setting its IP, gateway, netmask and MTU.
func SetupTUN(ifcName, ipCIDR, gateway string, mtu int) error {
	if err := run("ip", "a", "add", ipCIDR, "dev", ifcName); err != nil {
		return fmt.Errorf("error assigning IP: %w", err)
	}

	if err := run("ip", "link", "set", "dev", ifcName, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("error setting MTU: %w", err)
	}

	ip, _, err := parseCIDR(ipCIDR)
	if err != nil {
		return fmt.Errorf("error parsing IP CIDR: %w", err)
	}

	if err := run("ip", "link", "set", ifcName, "up"); err != nil {
		return fmt.Errorf("error setting interface up: %w", err)
	}

	if err := AddRoute(ip, gateway); err != nil {
		return fmt.Errorf("error setting gateway for interface: %w", err)
	}

	return nil
}

// AddRoute adds route to `ip` with `netmask` through the `gateway` to the OS routing table.
func AddRoute(ip, gateway string) error {
	return run("ip", "r", "add", ip, "via", gateway)
}

// DeleteRoute removes route to `ip` with `netmask` through the `gateway` from the OS routing table.
func DeleteRoute(ip, gateway string) error {
	return run("ip", "r", "del", ip, "via", gateway)
}

var setupOnce sync.Once

func setupSysPrivileges() (suid int, err error) {
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

func releaseSysPrivileges(_ int) error {
	return nil
}
