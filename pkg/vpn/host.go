// Package vpn pkg/vpn/host.go c4-app-vpn
package vpn

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// HostNetwork is the network of the computer a browser visor runs on, as a
// host bridge lends it: a TUN device there and its routing table. With
// ClientConfig.Host the client sets them up through it, as it sets up its
// own OS natively.
type HostNetwork interface {
	TUN(ctx context.Context) (TUNDevice, error)
	// Net runs "gateway", "setup <tun> <cidr> <mtu>" or
	// "route add|change|del <dst> <gateway>" on the host.
	Net(ctx context.Context, cmd string, args ...string) (string, error)
}

var errNoHostNetwork = errors.New("vpn: no host bridge connected to put the tunnel on the computer")

const hostTimeout = 20 * time.Second

var (
	hostMu  sync.Mutex
	hostNet HostNetwork
)

// SetHostNetwork installs the host network a browser visor's client uses
// with ClientConfig.Host. Nil removes it.
func SetHostNetwork(h HostNetwork) {
	hostMu.Lock()
	hostNet = h
	hostMu.Unlock()
}

func hostNetCall(cmd string, args ...string) (string, error) {
	hostMu.Lock()
	h := hostNet
	hostMu.Unlock()
	if h == nil {
		return "", errNoHostNetwork
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	return h.Net(ctx, cmd, args...)
}

func hostTUN() (TUNDevice, error) {
	hostMu.Lock()
	h := hostNet
	hostMu.Unlock()
	if h == nil {
		return nil, errNoHostNetwork
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	return h.TUN(ctx)
}

func hostGateway() (net.IP, error) {
	s, err := hostNetCall("gateway")
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, errors.New("vpn: bad host gateway " + s)
	}
	return ip, nil
}

// deadTUN reports a host TUN whose bridge session ended, which the next
// session replaces.
func deadTUN(t TUNDevice) bool {
	d, ok := t.(interface{ Dead() bool })
	return ok && d.Dead()
}

// lookupIP resolves on the host when a host network is installed. A
// browser has no resolver of its own, and the host's is the one it uses.
func lookupIP(name string) ([]net.IP, error) {
	hostMu.Lock()
	h := hostNet
	hostMu.Unlock()
	if h == nil {
		return net.LookupIP(name)
	}
	s, err := hostNetCall("resolve", name)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, f := range strings.Fields(s) {
		if ip := net.ParseIP(f); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}
