// Package app wraps the Skywire app client for the skydex-client application,
// mirroring internal/skydex-market/app.
package app

import (
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
)

// Client wraps the Skywire app client with convenience helpers.
type Client struct {
	*app.Client
}

// NewClient creates a new skydex-client app client that connects to the visor.
func NewClient() *Client {
	return &Client{Client: app.NewClient(nil)}
}

// Close closes the app client connection to the visor.
func (c *Client) Close() {
	if c.Client != nil {
		c.Client.Close()
	}
}

// LogInfo logs an info message through the visor's logging system.
func (c *Client) LogInfo(format string, args ...any) {
	if c.Client != nil {
		c.Client.Log().Infof(format, args...)
	}
}

// LogError logs an error message through the visor's logging system.
func (c *Client) LogError(format string, args ...any) {
	if c.Client != nil {
		c.Client.Log().Errorf(format, args...)
	}
}

// SetErrorOrLog sets an error status on the visor or logs it if that fails.
func (c *Client) SetErrorOrLog(err error) {
	if c.Client != nil {
		c.Client.SetErrorOrLog(err)
	}
}

// SetStatusOrLog sets a status on the visor or logs it if that fails.
func (c *Client) SetStatusOrLog(status appserver.AppDetailedStatus) {
	if c.Client != nil {
		c.Client.SetStatusOrLog(status)
	}
}

// WorkDir returns the working directory for this app as provided by the visor.
func (c *Client) WorkDir() string {
	if c.Client != nil {
		return c.Client.Config().ProcWorkDir
	}
	return ""
}

// RoutingPort returns the routing port assigned to this app by the visor.
func (c *Client) RoutingPort() uint16 {
	if c.Client != nil {
		return uint16(c.Client.Config().RoutingPort)
	}
	return 0
}

// VisorPubKey returns the public key of the visor this app runs on.
func (c *Client) VisorPubKey() string {
	if c.Client != nil {
		return c.Client.Config().VisorPK.Hex()
	}
	return ""
}
