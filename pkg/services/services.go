// Package services pkg/services/services.go
//
// Multi-service-in-one-process runner for Skywire deployment services.
// Each service registers a Factory that decodes its block from a
// JSON services.json and returns a Service that runs until ctx
// cancels or a fatal error.
//
// Goal: collapse the nine deployment-side containers exercised by
// CI e2e (dmsg-discovery, dmsg-server, transport-discovery,
// service-discovery, address-resolver, route-finder, transport-setup,
// stun-server, setup-node) down to a single binary invocation
// (`skywire svc run --config services.json`) so CI e2e doesn't have
// to wrangle nine Dockerfiles. uptime-tracker and network-monitor
// are out of scope — UT is being subsumed by TPD, NM is intentionally
// disabled in the production deployment. Production deployments
// unchanged — the existing per-service cobra subcommands stay and
// build their own Service via the same Factory.
//
// Topology choice: each service gets its own HTTP listener (no path
// multiplexing). In-process topology stays identical to the
// multi-container topology, so the e2e suite exercises the same code
// paths whether it runs collapsed or not.
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/skycoin/skywire/pkg/logging"
)

// Service is what each per-service package returns from its Factory.
// Run blocks until ctx is canceled or a fatal error occurs. The
// registry shuts services down by canceling the shared ctx — there's
// no separate Stop method.
type Service interface {
	Run(ctx context.Context) error
}

// Factory builds a Service from a raw block in services.json. The
// block is the full JSON object the operator wrote (including the
// "type" field, which factories typically ignore — the registry has
// already used it to dispatch).
type Factory func(raw json.RawMessage, log *logging.Logger) (Service, error)

// File is the on-disk shape of services.json.
//
//	{
//	  "services": [
//	    { "type": "dmsg-discovery", ... },
//	    { "type": "dmsg-server",    ... }
//	  ]
//	}
//
// Each Block carries its full original JSON so the per-service
// factory can decode into its own typed config struct without losing
// fields the framework doesn't know about.
type File struct {
	Services []Block `json:"services"`
}

// Block is one service entry. UnmarshalJSON pulls the type marker
// out and stashes the full block bytes on Raw for the factory.
type Block struct {
	Type string
	Name string
	Raw  json.RawMessage
}

// UnmarshalJSON captures both the typed fields ("type", optional
// "name") and the full block bytes, so factories can re-decode the
// block into their own config struct. Forward-compatible: extra
// service-specific fields live alongside "type" / "name" in the same
// object and reach the factory via Raw.
func (b *Block) UnmarshalJSON(data []byte) error {
	var probe struct {
		Type string `json:"type"`
		Name string `json:"name,omitempty"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("services: parse block header: %w", err)
	}
	if probe.Type == "" {
		return fmt.Errorf("services: block missing required \"type\" field")
	}
	b.Type = probe.Type
	b.Name = probe.Name
	b.Raw = make(json.RawMessage, len(data))
	copy(b.Raw, data)
	return nil
}

// Label returns the operator-visible name for this block, falling
// back to the type when no explicit name was set. Used in log
// prefixes so an operator running multiple instances of the same
// service type can tell them apart.
func (b *Block) Label() string {
	if b.Name != "" {
		return b.Name
	}
	return b.Type
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register installs a Factory for the given service type. Called
// from each service package's init(); duplicate registrations panic
// (programmer error, caught at process startup).
func Register(svcType string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[svcType]; dup {
		panic(fmt.Sprintf("services: duplicate registration for type %q", svcType))
	}
	registry[svcType] = f
}

// Lookup returns the Factory registered for svcType, or false when
// none is registered. Exported so the cobra subcommand can produce a
// helpful "available types: ..." error before attempting a build.
func Lookup(svcType string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[svcType]
	return f, ok
}

// RegisteredTypes returns the sorted list of currently-registered
// service types. Used by the cobra subcommand's error messages and
// `skywire svc run --list` output.
func RegisteredTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sortStrings(out)
	return out
}

// sortStrings is a small dependency-free sort to avoid pulling in
// "sort" just for one call site.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
