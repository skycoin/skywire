package ptycfg

import (
	"context"

	"github.com/SkycoinProject/dmsg/cipher"
)

// GatewayName is the RPC gateway name for 'Cfg' type requests.
const GatewayName = "CfgGateway"

// Gateway is the configuration gateway.
type Gateway struct {
	ctx  context.Context
	auth Whitelist
}

// NewGateway creates a new configuration gateway.
func NewGateway(ctx context.Context, auth Whitelist) *Gateway {
	return &Gateway{ctx: ctx, auth: auth}
}

// Whitelist obtains the whitelist entries.
func (g *Gateway) Whitelist(_ *struct{}, out *[]cipher.PubKey) error {
	pks, err := g.auth.All()
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
func (g *Gateway) WhitelistAdd(in *[]cipher.PubKey, _ *struct{}) error {
	return g.auth.Add(*in...)
}

// WhitelistRemove removes a whitelist entry.
func (g *Gateway) WhitelistRemove(in *[]cipher.PubKey, _ *struct{}) error {
	return g.auth.Remove(*in...)
}
