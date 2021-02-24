package service

import (
	"fmt"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/skycoin/src/cipher"
)

type RPCGateway struct {
	dmsgC *dmsg.Client
}

type TransportReqStatus string

const (
	StatusProcessing TransportReqStatus = "processing"
	StatusError                         = "error"
	StatusExists                        = "exists"
)

type Result struct {
	Status TransportReqStatus
	Error  error
}

// todo: look for an existing type, smth akin to TransportEntry
type TransportRequest struct {
	Nodes         [2]cipher.PubKey
	TransportType string
}

func NewGateway(conf Config) *RPCGateway {
	disc := disc.NewHTTP(conf.Dmsg.Discovery)
	dmsgConf := &dmsg.Config{MinSessions: conf.Dmsg.SessionsCount}
	dmsgC := dmsg.NewClient(conf.PK, conf.SK, disc, dmsgConf)
	return &RPCGateway{dmsgC: dmsgC}
}

func AddTransport(req TransportRequest, res *Result) error {
	// todo: dial visor via dmsg and call RPC method on it
	// todo: call visor rpc method for adding a transport
	res.Error = fmt.Errorf("not implemented")
	return nil
}

type TestGateway struct{}

type TestResult struct {
	Text string
}

type TestRequest struct {
	Text string
}

func (r *TestGateway) TestCall(req TestRequest, res *TestResult) error {
	res.Text = "rpc response" + req.Text
	return nil
}
