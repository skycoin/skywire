//go:build !js

// Package spec pkg/dmsgc/spec/spec_native.go
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
		var single Deployment
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		c.Deployments = []Deployment{single}
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
	if len(deployments) == 0 && (c.Discovery != "" || c.DiscoveryDmsg != "" || len(c.Servers) > 0 || c.SessionsCount != 0 || c.ConnectedServersType != "" || c.Protocol != "" || len(c.LANServers) > 0 || c.HypervisorDiscovery != "") {
		deployments = []Deployment{c.toDeployment()}
	}
	if len(deployments) == 1 {
		return json.Marshal(deployments[0])
	}
	return json.Marshal(deployments)
}
