//go:build ios

// Package netutil pkg/netutil/net_ios.go c0-com-util
package netutil

// DefaultNetworkInterface fetches the default network interface name. iOS has
// no shell and an app may not run route(8), so it asks the kernel instead
// (interfaceOfDefaultRoute) and, when there is no default route, falls back
// as Android does to the first usable interface, or "" for unknown.
func DefaultNetworkInterface() (string, error) {
	name, err := interfaceOfDefaultRoute()
	if err != nil || name != "" {
		return name, err
	}
	return firstRoutableInterface()
}
