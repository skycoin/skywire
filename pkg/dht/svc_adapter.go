package dht

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// svcSalt is the DHT namespace for service-discovery records.
//
// Service-discovery (SD) writes one DHT entry per visor at this single salt;
// the value is the full list of services that visor offers. This avoids the
// overwrite hazard of the older per-type scheme (a visor running both vpn
// and skysocks would write twice to the same target if the salt were
// identical and only differed by an out-of-key Type field). See
// pkg/service-discovery/api/api.go: dhtMirror documentation.
var svcSalt = []byte("svc")

// SvcAdapter publishes and queries service records via the DHT.
type SvcAdapter struct {
	node *Node
	log  *logging.Logger
}

// NewSvcAdapter creates a service discovery adapter backed by the DHT.
func NewSvcAdapter(node *Node, log *logging.Logger) *SvcAdapter {
	return &SvcAdapter{node: node, log: log}
}

// Register publishes the visor's full service list to the DHT.
//
// All of a visor's services live under one DHT target; callers must publish
// the complete list (vpn + skysocks + visor + …) on every change. Passing
// an empty slice clears the entry (see Deregister).
func (d *SvcAdapter) Register(ctx context.Context, services []servicedisc.Service, seq uint64) error {
	data, err := json.Marshal(services)
	if err != nil {
		return fmt.Errorf("dht svc: marshal: %w", err)
	}
	return d.node.Put(ctx, data, seq, svcSalt)
}

// Lookup retrieves a single service of the given type for a visor PK.
//
// Returns nil with no error if the visor has no service of that type
// (the DHT entry exists but the type isn't in the list); returns the
// underlying error if the DHT lookup itself fails.
func (d *SvcAdapter) Lookup(ctx context.Context, pk cipher.PubKey, serviceType string) (*servicedisc.Service, error) {
	services, err := d.LookupAll(ctx, pk)
	if err != nil {
		return nil, err
	}
	for i := range services {
		if services[i].Type == serviceType {
			return &services[i], nil
		}
	}
	return nil, nil
}

// LookupAll retrieves the full service list for a visor PK.
func (d *SvcAdapter) LookupAll(ctx context.Context, pk cipher.PubKey) ([]servicedisc.Service, error) {
	item, err := d.node.Get(ctx, pk, svcSalt)
	if err != nil {
		return nil, err
	}
	var services []servicedisc.Service
	if err := json.Unmarshal(item.V, &services); err != nil {
		return nil, fmt.Errorf("dht svc: unmarshal: %w", err)
	}
	return services, nil
}

// Deregister clears the visor's service list by publishing an empty array
// at a fresh seq.
func (d *SvcAdapter) Deregister(ctx context.Context, seq uint64) error {
	return d.node.Put(ctx, []byte("[]"), seq, svcSalt)
}

// AddressRecord is a DHT-native address resolver entry.
// Salt: "addr"
type AddressRecord struct {
	PK        cipher.PubKey `json:"pk"`
	Addresses []string      `json:"addresses,omitempty"` // e.g., ["1.2.3.4:30045"]
	Port      string        `json:"port,omitempty"`      // STCPR listen port
	IsLocal   bool          `json:"is_local,omitempty"`
}

// AddrSalt is the salt for address resolver entries.
var addrSalt = []byte("addr")

// AddrAdapter publishes and queries address records via the DHT.
type AddrAdapter struct {
	node *Node
	log  *logging.Logger
}

// NewAddrAdapter creates an address resolver adapter backed by the DHT.
func NewAddrAdapter(node *Node, log *logging.Logger) *AddrAdapter {
	return &AddrAdapter{node: node, log: log}
}

// Publish publishes an address record to the DHT.
func (d *AddrAdapter) Publish(ctx context.Context, rec AddressRecord, seq uint64) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("dht addr: marshal: %w", err)
	}
	return d.node.Put(ctx, data, seq, addrSalt)
}

// Resolve retrieves an address record for a given PK.
func (d *AddrAdapter) Resolve(ctx context.Context, pk cipher.PubKey) (*AddressRecord, error) {
	item, err := d.node.Get(ctx, pk, addrSalt)
	if err != nil {
		return nil, err
	}
	var rec AddressRecord
	if err := json.Unmarshal(item.V, &rec); err != nil {
		return nil, fmt.Errorf("dht addr: unmarshal: %w", err)
	}
	return &rec, nil
}
