// Package manager pkg/manager/rpc.go
package manager

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// RPC that exposes Management API methods to be used via RPC
type RPC struct {
	tm   *transport.Manager
	log  *logging.Logger
	ecdh []byte
}

// TransportRequest to perform an action over RPC
type TransportRequest struct {
	RemotePK cipher.PubKey
	Type     network.Type
}

// UUIDRequest contains id in UUID format
type UUIDRequest struct {
	ID uuid.UUID
}

// TransportResponse specifies an existing transport to remote node
type TransportResponse struct {
	ID     uuid.UUID
	Local  cipher.PubKey
	Remote cipher.PubKey
	Type   network.Type
}

// BoolResponse is a simple boolean wrapped in structure for RPC responses
type BoolResponse struct {
	Result bool
}

// AddTransport specified by request
func (r *RPC) AddTransport(req TransportRequest, res *TransportResponse) error {
	// enc := EncodeToBytes(TransportRequest{})
	// gw.chacha20poly1305.Encrypt(enc, gw.ecdh)

	// cryptoType := skyCrypto.CryptoTypeScryptChacha20poly1305
	ctx := context.Background()
	r.log.WithField("PK", req.RemotePK).WithField("type", req.Type).Info("Adding transport")
	tp, err := r.tm.SaveTransport(ctx, req.RemotePK, req.Type, transport.LabelSkycoin)
	if err != nil {
		r.log.WithError(err).Error("Cannot save transport")
		return err
	}
	res.ID = tp.Entry.ID
	res.Local = r.tm.Local()
	res.Remote = tp.Remote()
	res.Type = tp.Type()
	return nil
}

// ErrIncorrectType is returned when transport was not created by transport setup
var ErrIncorrectType = errors.New("transport was not created by skycoin")

// RemoveTransport removes all transports that match given request
func (r *RPC) RemoveTransport(req UUIDRequest, res *BoolResponse) error {
	r.log.WithField("ID", req.ID).Debug("Removing transport")
	tr, err := r.tm.GetTransportByID(req.ID)
	if err != nil {
		return err
	}
	if tr.Entry.Label != transport.LabelSkycoin {
		return ErrIncorrectType
	}
	r.tm.DeleteTransport(req.ID)
	res.Result = true
	return nil
}

// GetTransports returns all transports of this node that have been established by transport setup system
func (r *RPC) GetTransports(_ struct{}, res *[]TransportResponse) error {
	tps := r.tm.GetTransportsByLabel(transport.LabelSkycoin)
	for _, tp := range tps {
		tResp := TransportResponse{
			ID:     tp.Entry.ID,
			Local:  r.tm.Local(),
			Remote: tp.Remote(),
			Type:   tp.Type(),
		}
		*res = append(*res, tResp)
	}
	return nil
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
