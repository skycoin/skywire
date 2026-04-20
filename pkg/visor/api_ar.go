package visor

import (
	"context"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

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
