//+build linux

package vpn

import (
	"bytes"
	"fmt"
	"net"
	"sync"

	"github.com/syndtr/gocapability/capability"
	"golang.org/x/sys/unix"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	defaultNetworkGatewayCMD = `ip r | grep "default via" | awk '{print $3}'`
)

// DefaultNetworkGateway fetches system's default network gateway.
func DefaultNetworkGateway() (net.IP, error) {
	outBytes, err := osutil.RunWithResult("sh", "-c", defaultNetworkGatewayCMD)
	if err != nil {
		return nil, err
	}

	outBytes = bytes.TrimRight(outBytes, "\n")

	outLines := bytes.Split(outBytes, []byte{'\n'})

	for _, l := range outLines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			// initially look for IPv4 address
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, errCouldFindDefaultNetworkGateway
}

var setupClientOnce sync.Once

func setupClientSysPrivileges() (int, error) {
	var err error
	setupClientOnce.Do(func() {
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
		err = caps.Apply(capability.CAPS | capability.BOUNDS | capability.AMBIENT)
		if err != nil {
			err = fmt.Errorf("failed to apply capabilties: %w", err)

			return
		}

		// let child process keep caps sets from the parent, so we may do calls to
		// system utilities with these caps.
		err = unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0)
		if err != nil {
			err = fmt.Errorf("failed to set PR_SET_KEEPCAPS: %w", err)
			return
		}
	})

	return 0, nil
}

func releaseClientSysPrivileges(_ int) error {
	return nil
}
