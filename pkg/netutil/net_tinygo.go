//go:build tinygo

// Package netutil pkg/netutil/net_tinygo.go
//
// TinyGo stubs for the network-interface-enumeration + HTTP-probe helpers in
// net_native.go. TinyGo's net.Interface has no Addrs() and net/http doesn't
// compile on the wasm (js) target, so a TinyGo build can't enumerate host NICs.
// A TinyGo dmsg client doesn't need any of this (it has no host interfaces); the
// stubs exist so transitive linker references resolve. See
// docs/design/tinygo-dmsg-client.md.
package netutil

import (
	"errors"
	"net"
)

var errNoNetInterfaces = errors.New("netutil: network-interface enumeration unsupported in this build (TinyGo)")

// LocalNetworkInterfaceIPs returns the no-interfaces sentinel under TinyGo.
func LocalNetworkInterfaceIPs() ([]net.IP, error) { return nil, errNoNetInterfaces }

// NetworkInterfaceIPs returns the no-interfaces sentinel under TinyGo.
func NetworkInterfaceIPs(string) ([]net.IP, error) { return nil, errNoNetInterfaces }

// DefaultNetworkInterfaceIPs returns the no-interfaces sentinel under TinyGo.
func DefaultNetworkInterfaceIPs() ([]net.IP, error) { return nil, errNoNetInterfaces }

// HasPublicIP reports false under TinyGo (no way to enumerate interfaces).
func HasPublicIP() (bool, error) { return false, nil }

// LocalAddresses returns an empty list under TinyGo.
func LocalAddresses() ([]string, error) { return nil, nil }

// LocalProtocol reports false under TinyGo (no HTTP probe).
func LocalProtocol() bool { return false }
