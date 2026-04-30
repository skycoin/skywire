package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/netutil"
)

// DmsgPorterStatus contains DMSG porter diagnostics.
type DmsgPorterStatus struct {
	MainPorts int `json:"main_ports"`
	RSNPorts  int `json:"rsn_ports,omitempty"`
	MainFreed int `json:"main_freed,omitempty"`
	RSNFreed  int `json:"rsn_freed,omitempty"`
}

// DmsgPorterStats returns the current ephemeral port reservation
// counts for the main and embedded RSN DMSG clients.
func (v *Visor) DmsgPorterStats() (*DmsgPorterStatus, error) {
	s := &DmsgPorterStatus{}
	if v.dmsgC != nil {
		s.MainPorts = v.dmsgC.PorterCount()
	}
	if v.embeddedRouteSetup != nil && v.embeddedRouteSetup.DmsgClient() != nil {
		s.RSNPorts = v.embeddedRouteSetup.DmsgClient().PorterCount()
	}
	return s, nil
}

// DmsgPorterReset frees all ephemeral port reservations on the main
// and embedded RSN DMSG clients. Returns the number of ports freed.
func (v *Visor) DmsgPorterReset() (*DmsgPorterStatus, error) {
	s := &DmsgPorterStatus{}
	if v.dmsgC != nil {
		s.MainFreed = v.dmsgC.ResetPorter()
		s.MainPorts = v.dmsgC.PorterCount()
	}
	if v.embeddedRouteSetup != nil && v.embeddedRouteSetup.DmsgClient() != nil {
		s.RSNFreed = v.embeddedRouteSetup.DmsgClient().ResetPorter()
		s.RSNPorts = v.embeddedRouteSetup.DmsgClient().PorterCount()
	}
	return s, nil
}

// DmsgSetMinSessions updates the minimum DMSG session count at runtime.
func (v *Visor) DmsgSetMinSessions(n int) error {
	if v.dmsgC == nil {
		return fmt.Errorf("DMSG client not running")
	}
	v.dmsgC.SetMinSessions(n)
	return nil
}

// DmsgReconnect forces all DMSG sessions to close and reconnect.
func (v *Visor) DmsgReconnect() (int, error) {
	if v.dmsgC == nil {
		return 0, fmt.Errorf("DMSG client not running")
	}
	return v.dmsgC.ForceReconnect(), nil
}

// DHTSync fetches items from a DHT full node and stores them locally.
// If remotePK is empty, uses the first available bootstrap peer.
//
// Paginates until the remote reports no more items: each batch advances
// the sinceSeq cursor to the highest Seq seen so far, so a single CLI
// invocation pulls the full inventory rather than just the first batch
// (capped at 1000 server-side).
func (v *Visor) DHTSync(remotePK string, salt string) (int, error) {
	if v.dhtNode == nil {
		return 0, fmt.Errorf("DHT node not running")
	}

	var targetPK cipher.PubKey
	if remotePK != "" {
		if err := targetPK.Set(remotePK); err != nil {
			return 0, fmt.Errorf("invalid PK: %w", err)
		}
	} else {
		// Use first available bootstrap peer.
		fullNodes := dht.FindFullNodes(context.Background(), v.dhtNode)
		if len(fullNodes) == 0 {
			return 0, fmt.Errorf("no full nodes available")
		}
		targetPK = fullNodes[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	stored := 0
	var cursor uint64
	for {
		resp, err := v.dhtNode.GetItemsFrom(ctx, targetPK, salt, cursor, 0)
		if err != nil {
			if stored > 0 {
				// Partial-progress: surface what we got rather than discarding.
				v.log.WithError(err).WithField("stored", stored).
					Warn("DHT sync: partial progress, remote call failed")
				return stored, nil
			}
			return 0, fmt.Errorf("sync from %s: %w", targetPK.String(), err)
		}

		var maxSeq uint64
		for i, item := range resp.Items {
			// Use PutMirror with the explicit target key from the response.
			// Mirrored items have item.K set to the mirror's PK (not the
			// subject PK), so item.Target() returns the wrong key.
			if i < len(resp.Targets) {
				v.dhtNode.Store().PutMirror(resp.Targets[i], item)
				stored++
			} else {
				// Fallback for old servers that don't send targets.
				if putErr := v.dhtNode.Store().Put(item); putErr == nil {
					stored++
				}
			}
			if item.Seq > maxSeq {
				maxSeq = item.Seq
			}
		}

		if !resp.HasMore || len(resp.Items) == 0 || maxSeq <= cursor {
			// !HasMore: server says we have everything.
			// no items: nothing came back, nothing more to do.
			// maxSeq <= cursor: defensive — would loop forever otherwise.
			break
		}
		cursor = maxSeq
	}

	v.log.WithField("stored", stored).Info("DHT sync result")

	return stored, nil
}

// DHTGetAll returns all DHT items matching the given salt as a JSON string.
// Requires the visor to have a DHT node (full or regular).
func (v *Visor) DHTGetAll(salt string) (string, error) {
	if v.dhtNode == nil {
		return "", fmt.Errorf("DHT node not running")
	}
	items, _, _ := v.dhtNode.Store().GetItems(salt, 0, 0)
	if len(items) == 0 {
		return "[]", nil
	}

	// Decode each item's value and collect into an array.
	var results []json.RawMessage
	for _, item := range items {
		results = append(results, json.RawMessage(item.V))
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal DHT items: %w", err)
	}
	return string(data), nil
}

// DmsgPorterDiag returns detailed diagnostic information about ephemeral
// port reservations in the embedded RSN's DMSG client porter.
func (v *Visor) DmsgPorterDiag() (*netutil.EphemeralDiagResult, error) {
	if v.embeddedRouteSetup == nil || v.embeddedRouteSetup.DmsgClient() == nil {
		return nil, fmt.Errorf("embedded RSN not running")
	}
	diag := v.embeddedRouteSetup.DmsgClient().PorterDiag()
	return &diag, nil
}
