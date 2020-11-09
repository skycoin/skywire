//+build darwin

package vpn

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"syscall"
)

const (
	defaultNetworkGatewayCMD = "netstat -rn | sed -n '/Internet/,/Internet6/p' | grep default | awk '{print $2}'"
)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	outputBytes, err := exec.Command("sh", "-c", defaultNetworkGatewayCMD).Output()
	if err != nil {
		return nil, fmt.Errorf("error running command %s: %w", defaultNetworkGatewayCMD, err)
	}

	outputBytes = bytes.TrimRight(outputBytes, "\n")

	lines := bytes.Split(outputBytes, []byte{'\n'})
	for _, l := range lines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, errCouldFindDefaultNetworkGateway
}

var clientSysPrivilegesMx sync.Mutex

func setupClientSysPrivileges() (suid int, err error) {
	clientSysPrivilegesMx.Lock()

	suid = syscall.Getuid()

	if err := syscall.Setuid(0); err != nil {
		return 0, fmt.Errorf("failed to setuid 0: %w", err)
	}

	return suid, nil
}

func releaseClientSysPrivileges(suid int) error {
	err := syscall.Setuid(suid)
	clientSysPrivilegesMx.Unlock()
	return err
}
