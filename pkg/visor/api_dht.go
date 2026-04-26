package visor

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
)

// DHTStatus contains status information about the DHT node.
type DHTStatus struct {
	Running          bool   `json:"running"`
	NodeID           string `json:"node_id"`
	RoutingPeers     int    `json:"routing_peers"`
	StoredItems      int    `json:"stored_items"`
	WhitelistedItems int    `json:"whitelisted_items"`
	TrustedItems     int    `json:"trusted_items"`
	PublicItems      int    `json:"public_items"`
	FullNode         bool   `json:"full_node"`
	// Lookup counters for DMSG client entry resolution.
	LookupCacheHits  int64 `json:"lookup_cache_hits"`
	LookupDHTHits    int64 `json:"lookup_dht_hits"`
	LookupHTTPHits   int64 `json:"lookup_http_hits"`
	LookupHTTPMisses int64 `json:"lookup_http_misses"`
}

// DHTStatus returns the current status of the DHT node.
func (v *Visor) DHTStatus() (*DHTStatus, error) {
	if v.dhtNode == nil {
		return &DHTStatus{Running: false}, nil
	}
	wl, tr, pub := v.dhtNode.Store().CountByTier()
	status := &DHTStatus{
		Running:          true,
		NodeID:           v.dhtNode.ID().String(),
		RoutingPeers:     v.dhtNode.RoutingTable().Size(),
		StoredItems:      v.dhtNode.Store().Len(),
		WhitelistedItems: wl,
		TrustedItems:     tr,
		PublicItems:      pub,
		FullNode:         v.dhtNode.Store().IsFullNode(),
	}
	if v.dmsgC != nil {
		status.LookupCacheHits = v.dmsgC.LookupCacheHits.Load()
		status.LookupDHTHits = v.dmsgC.LookupDHTHits.Load()
		status.LookupHTTPHits = v.dmsgC.LookupHTTPHits.Load()
		status.LookupHTTPMisses = v.dmsgC.LookupHTTPMisses.Load()
	}
	return status, nil
}

// DHTNetworkSize returns an estimate of the DHT network size based
// on the local routing table's peer distribution.
func (v *Visor) DHTNetworkSize() (int, error) {
	if v.dhtNode == nil {
		return 0, fmt.Errorf("DHT node not running")
	}
	return v.dhtNode.RoutingTable().Size(), nil
}

// DHTSetFullNode enables or disables full node mode at runtime.
func (v *Visor) DHTSetFullNode(full bool) error {
	if v.dhtNode == nil {
		return fmt.Errorf("DHT node not running")
	}
	v.dhtNode.Store().SetFullNode(full)
	return nil
}

// DHTPut publishes a value to the DHT under the visor's own key.
func (v *Visor) DHTPut(value []byte, seq uint64, salt string) error {
	if v.dhtNode == nil {
		return fmt.Errorf("DHT node not running")
	}
	return v.dhtNode.Put(context.Background(), value, seq, []byte(salt))
}

// DHTGet retrieves a value from the DHT by publisher public key and salt.
func (v *Visor) DHTGet(pk string, salt string) ([]byte, error) {
	if v.dhtNode == nil {
		return nil, fmt.Errorf("DHT node not running")
	}

	var pubKey cipher.PubKey
	if err := pubKey.Set(pk); err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	item, err := v.dhtNode.Get(context.Background(), pubKey, []byte(salt))
	if err != nil {
		return nil, err
	}
	return item.V, nil
}
