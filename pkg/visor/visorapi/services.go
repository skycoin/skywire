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
	Error     string `json:"error,omitempty"`
	services.State
}
