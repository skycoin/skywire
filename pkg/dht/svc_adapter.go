package dht

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// ServiceRecord is a DHT-native service advertisement.
// It mirrors the essential fields from servicedisc.Service but is
// independent of the HTTP service discovery types to avoid import cycles.
type ServiceRecord struct {
	PK      cipher.PubKey `json:"pk"`
	Port    uint16        `json:"port"`
	Type    string        `json:"type"`    // e.g., "vpn", "skysocks", "proxy"
	Version string        `json:"version"` // visor version
	Addr    string        `json:"addr,omitempty"`
}

// SvcAdapter publishes and queries service records via the DHT.
// Salt format: "svc:" + serviceType (e.g., "svc:vpn", "svc:skysocks").
type SvcAdapter struct {
	node *Node
	log  *logging.Logger
}

// NewSvcAdapter creates a service discovery adapter backed by the DHT.
func NewSvcAdapter(node *Node, log *logging.Logger) *SvcAdapter {
	return &SvcAdapter{node: node, log: log}
}

func svcSalt(serviceType string) []byte {
	return []byte("svc:" + serviceType)
}

// Register publishes a service record to the DHT.
func (d *SvcAdapter) Register(ctx context.Context, rec ServiceRecord, seq uint64) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("dht svc: marshal: %w", err)
	}
	return d.node.Put(ctx, data, seq, svcSalt(rec.Type))
}

// Lookup retrieves a service record for a given PK and service type.
func (d *SvcAdapter) Lookup(ctx context.Context, pk cipher.PubKey, serviceType string) (*ServiceRecord, error) {
	item, err := d.node.Get(ctx, pk, svcSalt(serviceType))
	if err != nil {
		return nil, err
	}
	var rec ServiceRecord
	if err := json.Unmarshal(item.V, &rec); err != nil {
		return nil, fmt.Errorf("dht svc: unmarshal: %w", err)
	}
	return &rec, nil
}

// Deregister removes a service record by publishing an empty tombstone.
func (d *SvcAdapter) Deregister(ctx context.Context, serviceType string, seq uint64) error {
	return d.node.Put(ctx, []byte("{}"), seq, svcSalt(serviceType))
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
