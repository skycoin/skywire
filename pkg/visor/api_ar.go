package visor

import (
	"context"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// ARSelfEntry is one transport-type-row of this visor's own AR registration:
// the IP:port it is reachable on according to the address-resolver.
type ARSelfEntry struct {
	Type       string   `json:"type"`                  // "stcpr" or "sudph"
	RemoteAddr string   `json:"remote_addr,omitempty"` // public IP as AR sees the visor
	Port       string   `json:"port,omitempty"`        // listen port
	Addresses  []string `json:"addresses,omitempty"`   // local interface addresses the visor advertised
}

// ARSelfRegistration is the visor's own AR record across transport types.
// One ARSelfEntry per transport the visor is currently registered for.
// Empty when the visor has not (yet) bound any transport with AR.
type ARSelfRegistration struct {
	Entries []ARSelfEntry `json:"entries,omitempty"`
}

// ARSelfInfo resolves the visor's own AR registration via its AR
// client, returning what AR currently holds for STCPR and SUDPH. The
// result reflects the canonical HTTP-side state — the same view the
// AR-→-DHT mirror writes from — so it's stable even when the local
// DHT cache hasn't yet caught up.
func (v *Visor) ARSelfInfo() (*ARSelfRegistration, error) {
	if v.tpM == nil {
		return nil, fmt.Errorf("transport manager not running")
	}
	arClient := v.tpM.ARClient()
	if arClient == nil {
		return nil, fmt.Errorf("address resolver client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out := &ARSelfRegistration{}
	for _, tpType := range []string{"stcpr", "sudph"} {
		data, err := arClient.Resolve(ctx, tpType, v.conf.PK)
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, arSelfEntryFromVisorData(tpType, data))
	}
	return out, nil
}

func arSelfEntryFromVisorData(tpType string, d addrresolver.VisorData) ARSelfEntry {
	return ARSelfEntry{
		Type:       tpType,
		RemoteAddr: d.RemoteAddr,
		Port:       d.Port,
		Addresses:  append([]string(nil), d.Addresses...),
	}
}

// CheckAREntry checks if a public key is registered in the address
// resolver. Returns the transport types the visor is registered for
// (e.g., ["stcpr", "sudph"]) without revealing its IP address.
func (v *Visor) CheckAREntry(pk string) ([]string, error) {
	if v.tpM == nil {
		return nil, fmt.Errorf("transport manager not running")
	}

	var pubKey cipher.PubKey
	if err := pubKey.Set(pk); err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	var types []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	arClient := v.tpM.ARClient()
	if arClient == nil {
		return nil, fmt.Errorf("address resolver client not available")
	}

	// Try each transport type — only report whether the entry exists,
	// not the resolved address (privacy).
	for _, tpType := range []string{"stcpr", "sudph"} {
		_, err := arClient.Resolve(ctx, tpType, pubKey)
		if err == nil {
			types = append(types, tpType)
		}
	}

	return types, nil
}
