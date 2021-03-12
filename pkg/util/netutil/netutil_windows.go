//+build windows

package netutil

import "errors"

var (
	errMethodNotSupported = errors.New("this methods is not supported for this OS")
)

// DefaultNetworkInterface fetches default network interface name.
func DefaultNetworkInterface() (string, error) {
	return "", errMethodNotSupported
}
