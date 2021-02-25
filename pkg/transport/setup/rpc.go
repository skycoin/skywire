package setup

import (
	"context"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

type TestGateway struct{}

type TestResult struct {
	Text string
}

type TestRequest struct {
	Text string
}

func (r *TestGateway) TestCall(req TestRequest, res *TestResult) error {
	res.Text = "visor response: " + req.Text
	return nil
}

type RPCGateway struct {
	tm *transport.Manager
}

type TransportRequest struct {
	RemotePK cipher.PubKey
	Type     string
}

type TransportResponse struct {
	ID     uuid.UUID
	Local  cipher.PubKey
	Remote cipher.PubKey
	Type   string
	IsUp   bool
}

func (gw *RPCGateway) AddTransport(req TransportRequest, res *TransportResponse) error {
	ctx := context.Background()
	mt, err := gw.tm.SaveTransport(ctx, req.RemotePK, req.Type)
	if err != nil {
		return err
	}
	res.ID = mt.Entry.ID
	res.Local = gw.tm.Local()
	res.Remote = mt.Remote()
	res.Type = mt.Type()
	res.IsUp = mt.IsUp()
	return nil
}
