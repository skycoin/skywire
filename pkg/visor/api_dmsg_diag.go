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
// Delegates pagination to dht.Node.SyncFrom, which is shared with the
// node's own periodic full-node pull loop so they have identical
// semantics.
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

	stored, err := v.dhtNode.SyncFrom(ctx, targetPK, salt)
	if err != nil {
		if stored > 0 {
			// Partial-progress: surface what we got rather than discarding.
			v.log.WithError(err).WithField("stored", stored).
				Warn("DHT sync: partial progress, remote call failed")
			return stored, nil
		}
		return 0, fmt.Errorf("sync from %s: %w", targetPK.String(), err)
	}

	v.log.WithField("stored", stored).Info("DHT sync result")

	return stored, nil
}

// DHTGetAll returns all DHT items matching the given salt as a JSON string.
// Requires the visor to have a DHT node (full or regular).
//
// Pages over the local store with a Seq cursor until exhausted so the
// returned array reflects every matching item rather than just the first
// 1000 (the default GetItems batch cap).
func (v *Visor) DHTGetAll(salt string) (string, error) {
	if v.dhtNode == nil {
		return "", fmt.Errorf("DHT node not running")
	}
	results := make([]json.RawMessage, 0)
	var cursor uint64
	for {
		items, _, hasMore := v.dhtNode.Store().GetItems(salt, cursor, 0)
		if len(items) == 0 {
			break
		}
		var maxSeq uint64
		for _, item := range items {
			results = append(results, json.RawMessage(item.V))
			if item.Seq > maxSeq {
				maxSeq = item.Seq
			}
		}
		if !hasMore || maxSeq <= cursor {
			break
		}
		cursor = maxSeq
	}
	if len(results) == 0 {
		return "[]", nil
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal DHT items: %w", err)
	}
	return string(data), nil
}

// DHTListWithTargets returns all DHT items matching the given salt as a JSON
// array of {target, value} objects, where target is the hex-encoded storage
// key (subject PK ⊕ salt hash) and value is the raw stored payload.
//
// Diff-friendly counterpart to DHTGetAll: callers comparing the DHT against
// HTTP discoveries need to know which subject PK each value belongs to,
// which the bare values do not always carry (e.g. a transport list under
// salt "tp" is keyed by edge PK but the JSON array of transports does not
// repeat the subject's own PK).
func (v *Visor) DHTListWithTargets(salt string) (string, error) {
	if v.dhtNode == nil {
		return "", fmt.Errorf("DHT node not running")
	}
	type withTarget struct {
		Target string          `json:"target"`
		Value  json.RawMessage `json:"value"`
	}
	results := make([]withTarget, 0)
	var cursor uint64
	for {
		items, targets, hasMore := v.dhtNode.Store().GetItems(salt, cursor, 0)
		if len(items) == 0 {
			break
		}
		var maxSeq uint64
		for i, item := range items {
			results = append(results, withTarget{
				Target: targets[i].String(),
				Value:  json.RawMessage(item.V),
			})
			if item.Seq > maxSeq {
				maxSeq = item.Seq
			}
		}
		if !hasMore || maxSeq <= cursor {
			break
		}
		cursor = maxSeq
	}
	if len(results) == 0 {
		return "[]", nil
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
