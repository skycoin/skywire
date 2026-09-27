//go:build !android

// Package vpn pkg/vpn/share_other.go c4-app-vpn
package vpn

// startSharing is a no-op off Android: nothing hands this client other
// devices' connections, so its serve loop stays exactly as it was.
func (c *Client) startSharing() (stop func()) { return nil }
