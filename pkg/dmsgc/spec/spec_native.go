//go:build !js

// Package spec pkg/dmsgc/spec/spec_native.go c1-net-dmsg
//
// DmsgConfig's MarshalJSON / UnmarshalJSON — tagged off the WASM
// path because encoding/json drags reflect runtime helpers
// TinyGo's stdlib doesn't ship. See spec.go's package doc for the
// broader rationale.
package spec

import (
	"bytes"
	"encoding/json"
)

// deploymentWithServer inlines a Deployment's fields (embedded, no json
// tag, so its exported fields are promoted into the surrounding object)
// and adds the visor-wide `server` key. It is the single-object wire
// shape so the in-process dmsg-server config round-trips alongside the
// primary deployment's fields. The array (multi-deployment) shape has no
// sibling slot for a top-level key, so Server is only (de)serialized in
// the single-object shape.
type deploymentWithServer struct {
	Deployment
	Server *DmsgServerConfig `json:"server,omitempty"`
}

// UnmarshalJSON accepts either a single Deployment object or an
// array of Deployments and populates Deployments + the legacy
// top-level mirror fields accordingly.
func (c *DmsgConfig) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(data, &c.Deployments); err != nil {
			return err
		}
	} else {
		var single deploymentWithServer
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		c.Deployments = []Deployment{single.Deployment}
		c.Server = single.Server
	}
	c.mirrorPrimary()
	return nil
}

// MarshalJSON emits the single-deployment object shape when there
// is exactly one deployment, otherwise the array shape. When
// Deployments is empty but the top-level fields are populated
// (e.g. a writer that only set Discovery + Servers directly), a
// one-element Deployments is synthesized from the mirror fields
// so the JSON output is correct.
func (c DmsgConfig) MarshalJSON() ([]byte, error) {
	deployments := c.Deployments
	if len(deployments) == 0 && (c.Discovery != "" || c.DiscoveryDmsg != "" || len(c.Servers) > 0 || c.SessionsCount != 0 || c.ConnectedServersType != "" || c.Protocol != "" || len(c.LANServers) > 0 || c.HypervisorDiscovery != "" || c.Server != nil) {
		deployments = []Deployment{c.toDeployment()}
	}
	if len(deployments) == 1 {
		return json.Marshal(deploymentWithServer{Deployment: deployments[0], Server: c.Server})
	}
	return json.Marshal(deployments)
}
