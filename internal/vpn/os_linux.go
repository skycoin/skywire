//+build linux

package vpn

import (
	"fmt"
	"strconv"
	"syscall"
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

var (
	errEAGAIN error = syscall.EAGAIN
	errEINVAL error = syscall.EINVAL
	errENOENT error = syscall.ENOENT
)

// errnoErr returns common boxed Errno values, to prevent
// allocations at runtime.
func errnoErr(e syscall.Errno) error {
	switch e {
	case 0:
		return nil
	case syscall.EAGAIN:
		return errEAGAIN
	case syscall.EINVAL:
		return errEINVAL
	case syscall.ENOENT:
		return errENOENT
	}
	return e
}

// Setuid sets uid of current OS user.
func Setuid(uid int) (err error) {
	// due to iss. 1435 `syscall.Setuid` is disabled for Linux system, so we'
	_, _, e1 := syscall.RawSyscall(syscall.SYS_SETUID, uintptr(uid), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
