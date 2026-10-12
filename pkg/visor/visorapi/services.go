// Package visorapi pkg/visor/visorapi/services.go
package visorapi

import "github.com/skycoin/skywire/pkg/services"

// EmbeddedServiceState is one deployment service running inside the visor
// (config.embedded_services), as `skywire cli visor state` reports it.
type EmbeddedServiceState struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// URL is where the service answers over dmsg: the visor's key, its
	// dmsg HTTP port and the service's path prefix.
	URL string `json:"url"`
	// PlainHTTP is the block's plain-HTTP address, if it serves one.
	PlainHTTP string `json:"plain_http,omitempty"`
	Running   bool   `json:"running"`
	// OwnKey is set when the service runs under its own key rather than the
	// visor's, with its own dmsg client; Restarts counts its restarts.
	OwnKey   bool `json:"own_key,omitempty"`
	Restarts int  `json:"restarts,omitempty"`
	// Stopped is set while an operator has stopped it.
	Stopped bool   `json:"stopped,omitempty"`
	Error   string `json:"error,omitempty"`
	services.State
}

// EmbeddedServiceControlArgs names an embedded service and what to do with
// it: stop, start or restart.
type EmbeddedServiceControlArgs struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}
