// Package visorapi pkg/visor/visorapi/alerts.go c3-vis-core
package visorapi

import (
	"github.com/skycoin/skywire/pkg/alerts"
)

// Alerts is the visor's offline alerts for the visors it watches
// (pkg/alerts).
type Alerts interface {
	// AlertsStatus returns the settings, without the relay password, and
	// the state of every watched visor.
	AlertsStatus() (*alerts.Status, error)
	// AlertsSet replaces the settings at once and saves them. An empty
	// relay password keeps the current one.
	AlertsSet(s alerts.Settings) error
	// AlertsTest sends a test alert to every configured channel.
	AlertsTest() ([]alerts.Result, error)
}

// AlertsStatus calls AlertsStatus.
func (rc *rpcClient) AlertsStatus() (*alerts.Status, error) {
	var out alerts.Status
	err := rc.Call("AlertsStatus", &struct{}{}, &out)
	return &out, err
}

// AlertsSet calls AlertsSet.
func (rc *rpcClient) AlertsSet(s alerts.Settings) error {
	return rc.Call("AlertsSet", &s, &struct{}{})
}

// AlertsTest calls AlertsTest.
func (rc *rpcClient) AlertsTest() ([]alerts.Result, error) {
	var out []alerts.Result
	err := rc.Call("AlertsTest", &struct{}{}, &out)
	return out, err
}

// AlertsStatus implements API.
func (mc *mockRPCClient) AlertsStatus() (*alerts.Status, error) { return &alerts.Status{}, nil }

// AlertsSet implements API.
func (mc *mockRPCClient) AlertsSet(alerts.Settings) error { return nil }

// AlertsTest implements API.
func (mc *mockRPCClient) AlertsTest() ([]alerts.Result, error) { return nil, nil }
