// Package app internal/app/app.go
package app

import (
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
)

// Client wraps the Skywire app client and provides convenient methods
// for the skydex-market application.
type Client struct {
	*app.Client
}

// NewClient creates a new skydex-market app client that connects to the visor.
func NewClient() *Client {
	appCl := app.NewClient(nil)
	return &Client{Client: appCl}
}

// Close closes the app client connection to the visor.
func (c *Client) Close() {
	if c.Client != nil {
		c.Client.Close()
	}
}

// LogInfo logs an info message through the visor's logging system.
func (c *Client) LogInfo(format string, args ...interface{}) {
	if c.Client != nil {
		c.Client.Log().Infof(format, args...)
	}
}

// LogError logs an error message through the visor's logging system.
func (c *Client) LogError(format string, args ...interface{}) {
	if c.Client != nil {
		c.Client.Log().Errorf(format, args...)
	}
}

// LogWarn logs a warning message through the visor's logging system.
func (c *Client) LogWarn(format string, args ...interface{}) {
	if c.Client != nil {
		c.Client.Log().Warnf(format, args...)
	}
}

// LogDebug logs a debug message through the visor's logging system.
func (c *Client) LogDebug(format string, args ...interface{}) {
	if c.Client != nil {
		c.Client.Log().Debugf(format, args...)
	}
}

// SetErrorOrLog sets an error status on the visor or logs it if status setting fails.
func (c *Client) SetErrorOrLog(err error) {
	if c.Client != nil {
		c.Client.SetErrorOrLog(err)
	}
}

// SetStatusOrLog sets a status on the visor or logs it if status setting fails.
func (c *Client) SetStatusOrLog(status appserver.AppDetailedStatus) {
	if c.Client != nil {
		c.Client.SetStatusOrLog(status)
	}
}

// SetOTPOrLog publishes the operator-panel one-time code to the visor, where
// it surfaces on the hypervisor's app list. Nil-safe like the helpers above so
// the market still runs standalone (no visor), just without a published code.
func (c *Client) SetOTPOrLog(otp string) {
	if c.Client != nil {
		c.Client.SetOTPOrLog(otp)
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

// VisorPubKey returns the public key of the visor this app is running on.
func (c *Client) VisorPubKey() string {
	if c.Client != nil {
		return c.Client.Config().VisorPK.Hex()
	}
	return ""
}
