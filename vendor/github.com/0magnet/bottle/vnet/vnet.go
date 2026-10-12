// Package vnet vnet/vnet.go
//
// Address namespaces, the same in every build. "vnet:" names this runtime's
// own loopback, the one a plain loopback address already means: the page's
// virtual network in a browser, the OS loopback natively. "host:" names the
// computer's own network: the OS natively, and in a browser the network
// installed with SetHost, such as a host bridge. So "host:127.0.0.1:1080"
// is a port of the computer in both, and a config that uses it means the same
// thing in a browser tab and on a host.
package vnet

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// VnetPrefix names this runtime's own loopback.
	VnetPrefix = "vnet:"
	// HostPrefix names the computer's own network.
	HostPrefix = "host:"
)

// ErrNoHost is returned for a host address in a browser with no host
// network installed.
var ErrNoHost = errors.New("vnet: no host network to reach the computer through")

// HostNetwork opens sockets on the computer a browser runs on.
type HostNetwork interface {
	Listen(network, address string) (net.Listener, error)
	DialTimeout(network, address string, timeout time.Duration) (net.Conn, error)
}

var (
	hostMu  sync.RWMutex
	hostNet HostNetwork
)

// SetHost installs the network host addresses use in a browser. Nil removes
// it. Native builds reach the computer directly and ignore it.
func SetHost(h HostNetwork) {
	hostMu.Lock()
	hostNet = h
	hostMu.Unlock()
}

func currentHost() HostNetwork {
	hostMu.RLock()
	defer hostMu.RUnlock()
	return hostNet
}

// Split returns address without its namespace and whether it names the
// computer's own network.
func Split(address string) (addr string, host bool) {
	switch {
	case strings.HasPrefix(address, HostPrefix):
		return strings.TrimPrefix(address, HostPrefix), true
	case strings.HasPrefix(address, VnetPrefix):
		return strings.TrimPrefix(address, VnetPrefix), false
	}
	return address, false
}
