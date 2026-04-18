package visor

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// DHTStatus contains status information about the DHT node.
type DHTStatus struct {
	Running            bool   `json:"running"`
	NodeID             string `json:"node_id"`
	RoutingPeers       int    `json:"routing_peers"`
	StoredItems        int    `json:"stored_items"`
	WhitelistedItems   int    `json:"whitelisted_items"`
	TrustedItems       int    `json:"trusted_items"`
	PublicItems        int    `json:"public_items"`
	FullNode           bool   `json:"full_node"`
}

// DHTStatus returns the current status of the DHT node.
func (v *Visor) DHTStatus() (*DHTStatus, error) {
	if v.dhtNode == nil {
		return &DHTStatus{Running: false}, nil
	}
	wl, tr, pub := v.dhtNode.Store().CountByTier()
	return &DHTStatus{
		Running:          true,
		NodeID:           v.dhtNode.ID().String(),
		RoutingPeers:     v.dhtNode.RoutingTable().Size(),
		StoredItems:      v.dhtNode.Store().Len(),
		WhitelistedItems: wl,
		TrustedItems:     tr,
		PublicItems:      pub,
		FullNode:         v.conf.DHT != nil && v.conf.DHT.FullNode,
	}, nil
}

// DHTPut publishes a value to the DHT under the visor's own key.
func (v *Visor) DHTPut(value []byte, seq uint64, salt []byte) error {
	if v.dhtNode == nil {
		return fmt.Errorf("DHT node not running")
	}
	return v.dhtNode.Put(context.Background(), value, seq, salt)
}

// DHTGet retrieves a value from the DHT by publisher public key and salt.
func (v *Visor) DHTGet(pk string, salt []byte) ([]byte, error) {
	if v.dhtNode == nil {
		return nil, fmt.Errorf("DHT node not running")
	}

	var pubKey cipher.PubKey
	if err := pubKey.Set(pk); err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	item, err := v.dhtNode.Get(context.Background(), pubKey, salt)
	if err != nil {
		return nil, err
	}
	return item.V, nil
}
