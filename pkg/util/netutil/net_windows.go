//+build windows

package netutil

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	return "", errServerMethodsNotSupported
}
