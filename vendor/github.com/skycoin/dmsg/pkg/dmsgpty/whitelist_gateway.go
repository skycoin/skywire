package dmsgpty

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// WhitelistGateway is the configuration gateway.
type WhitelistGateway struct {
	wl Whitelist
}

// NewWhitelistGateway creates a new configuration gateway.
func NewWhitelistGateway(auth Whitelist) *WhitelistGateway {
	return &WhitelistGateway{wl: auth}
}

// Whitelist obtains the whitelist entries.
func (g *WhitelistGateway) Whitelist(_ *struct{}, out *[]cipher.PubKey) error {
	pks, err := g.wl.All()
	if err != nil {
		return err
	}
	*out = make([]cipher.PubKey, 0, len(pks))
	for pk, ok := range pks {
		if ok {
			*out = append(*out, pk)
		}
	}
	return nil
}

// WhitelistAdd adds a whitelist entry.
func (g *WhitelistGateway) WhitelistAdd(in *[]cipher.PubKey, _ *struct{}) error {
	return g.wl.Add(*in...)
}

// WhitelistRemove removes a whitelist entry.
func (g *WhitelistGateway) WhitelistRemove(in *[]cipher.PubKey, _ *struct{}) error {
	return g.wl.Remove(*in...)
}
