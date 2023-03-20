// Package manager pkg/manager/rpc.go
package manager

import (
	"fmt"
	"net/rpc"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/setup"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

const (
	// RPCPrefix is the prefix used with all RPC calls.
	RPCPrefix = "app-manager"
)

// RPC that exposes Management API methods to be used via RPC
type RPC struct {
	mgmt *ManagementInterface
	log  *logging.Logger
}

func newRPCServer(mgmt *ManagementInterface, log *logging.Logger) (*rpc.Server, error) {
	rpcS := rpc.NewServer()
	rpcG := &RPC{
		mgmt: mgmt,
		log:  log,
	}

	if err := rpcS.RegisterName(RPCPrefix, rpcG); err != nil {
		return nil, fmt.Errorf("failed to create visor RPC server: %w", err)
	}

	return rpcS, nil
}

// AddTransportIn is input for AddTransport.
type AddTransportIn struct {
	RemotePK cipher.PubKey
	TpType   string
	Timeout  time.Duration
}

// AddTransport creates a transport for the visor.
func (r *RPC) AddTransport(in *AddTransportIn, out *setup.TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "AddTransport", in)(out, &err)

	tp, err := r.mgmt.tpSetup.AddTransport(in.RemotePK, in.TpType, in.Timeout)
	if tp != nil {
		*out = *tp
	}

	return err
}

// RemoveTransport removes a Transport from the visor.
func (r *RPC) RemoveTransport(tid *uuid.UUID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveTransport", tid)(nil, &err)

	return r.mgmt.tpSetup.RemoveTransport(*tid)
}

// GetTransports returns all transports of this node that have been established by transport setup system
func (r *RPC) GetTransports(_ *struct{}, out *[]*setup.TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transports", nil)(out, &err)
	r.log.Error("suckla")
	transports, err := r.mgmt.tpSetup.GetTransports()
	*out = transports

	return err
}

// func encodeToBytes(p interface{}) []byte {
// 	buf := bytes.Buffer{}
// 	enc := gob.NewEncoder(&buf)
// 	err := enc.Encode(p)
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	return buf.Bytes()
// }
