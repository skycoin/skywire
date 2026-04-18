// Package visor pkg/visor/forwarded_ports.go
//
// ForwardedPorts manages the set of ports that are accessible over
// skynet and/or DMSG, with per-port metadata (label, description,
// visibility on the landing page, PK whitelist).
package visor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// ForwardedPort describes a single forwarded port with its metadata.
type ForwardedPort struct {
	Port          int             `json:"port"`
	Label         string          `json:"label,omitempty"`
	Description   string          `json:"description,omitempty"`
	ShowOnLanding bool            `json:"show_on_landing"`
	Whitelist     []cipher.PubKey `json:"whitelist,omitempty"` // empty = accessible to all authenticated peers
	Skynet        bool            `json:"skynet"`              // forward over skynet (sky-forwarding server)
	DMSG          bool            `json:"dmsg"`                // forward over DMSG (service registry)
}

// ForwardedPorts is a thread-safe collection of forwarded port definitions.
type ForwardedPorts struct {
	mu       sync.RWMutex
	ports    map[int]*ForwardedPort
	filePath string // persistence path; empty = in-memory only
}

// NewForwardedPorts creates a new ForwardedPorts store.
// If filePath is non-empty, the store loads from and saves to that file.
func NewForwardedPorts(filePath string) *ForwardedPorts {
	fp := &ForwardedPorts{
		ports:    make(map[int]*ForwardedPort),
		filePath: filePath,
	}
	if filePath != "" {
		fp.load() //nolint:errcheck,gosec
	}
	return fp
}

// Register adds or updates a forwarded port. Saves to disk if configured.
func (fp *ForwardedPorts) Register(p ForwardedPort) error {
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("invalid port %d", p.Port)
	}
	fp.mu.Lock()
	fp.ports[p.Port] = &p
	fp.mu.Unlock()
	return fp.save()
}

// Deregister removes a forwarded port.
func (fp *ForwardedPorts) Deregister(port int) error {
	fp.mu.Lock()
	delete(fp.ports, port)
	fp.mu.Unlock()
	return fp.save()
}

// Get returns a copy of a single port definition, or nil.
func (fp *ForwardedPorts) Get(port int) *ForwardedPort {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	p, ok := fp.ports[port]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// List returns all forwarded ports, sorted by port number.
func (fp *ForwardedPorts) List() []ForwardedPort {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	out := make([]ForwardedPort, 0, len(fp.ports))
	for _, p := range fp.ports {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// LandingPagePorts returns ports visible on the landing page, sorted.
func (fp *ForwardedPorts) LandingPagePorts() []ForwardedPort {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	var out []ForwardedPort
	for _, p := range fp.ports {
		if p.ShowOnLanding {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// IsSkynetAllowed checks if the given port is forwarded over skynet.
func (fp *ForwardedPorts) IsSkynetAllowed(port int) bool {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	p, ok := fp.ports[port]
	return ok && p.Skynet
}

// IsDMSGAllowed checks if the given port is forwarded over DMSG.
func (fp *ForwardedPorts) IsDMSGAllowed(port int) bool {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	p, ok := fp.ports[port]
	return ok && p.DMSG
}

// IsWhitelisted checks if the given PK is allowed to access the port.
// Returns true if the whitelist is empty (open to all) or the PK is in the list.
func (fp *ForwardedPorts) IsWhitelisted(port int, pk cipher.PubKey) bool {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	p, ok := fp.ports[port]
	if !ok {
		return false
	}
	if len(p.Whitelist) == 0 {
		return true // open to all
	}
	for _, wl := range p.Whitelist {
		if wl == pk {
			return true
		}
	}
	return false
}

// AllSkynetPorts returns port numbers that are forwarded over skynet.
// Used by the old allowed-ports check in the sky-forwarding server.
func (fp *ForwardedPorts) AllSkynetPorts() []int {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	var out []int
	for port, p := range fp.ports {
		if p.Skynet {
			out = append(out, port)
		}
	}
	sort.Ints(out)
	return out
}

// LandingPageEntries returns entries for the log server landing page.
// Satisfies logserver.ForwardedPortLister.
func (fp *ForwardedPorts) LandingPageEntries() []logserver.ForwardedPortEntry {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	var out []logserver.ForwardedPortEntry
	for _, p := range fp.ports {
		if p.ShowOnLanding {
			out = append(out, logserver.ForwardedPortEntry{
				Port:        p.Port,
				Label:       p.Label,
				Description: p.Description,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func (fp *ForwardedPorts) save() error {
	if fp.filePath == "" {
		return nil
	}
	fp.mu.RLock()
	data, err := json.MarshalIndent(fp.ports, "", "  ")
	fp.mu.RUnlock()
	if err != nil {
		return err
	}
	dir := filepath.Dir(fp.filePath)
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec
		return err
	}
	return os.WriteFile(fp.filePath, data, 0o600)
}

func (fp *ForwardedPorts) load() error {
	data, err := os.ReadFile(fp.filePath) //nolint:gosec
	if err != nil {
		return err // file doesn't exist yet — fine
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return json.Unmarshal(data, &fp.ports)
}
