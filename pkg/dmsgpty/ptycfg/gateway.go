package ptycfg

import (
	"context"

	"github.com/SkycoinProject/dmsg/cipher"
)

const GatewayName = "CfgGateway"

type Gateway struct {
	ctx  context.Context
	auth Whitelist
}

func NewGateway(ctx context.Context, auth Whitelist) *Gateway {
	return &Gateway{ctx: ctx, auth: auth}
}

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

func (g *Gateway) WhitelistAdd(in *[]cipher.PubKey, _ *struct{}) error {
	return g.auth.Add(*in...)
}

func (g *Gateway) WhitelistRemove(in *[]cipher.PubKey, _ *struct{}) error {
	return g.auth.Remove(*in...)
}
