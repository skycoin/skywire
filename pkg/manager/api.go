// Package manager pkg/manager/api.go
package manager

import (
	"github.com/skycoin/skywire/pkg/transport/setup"
)

// // API represents visor API.
// type API interface {
// 	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*setup.TransportSummary, error)
// 	RemoveTransport(tid uuid.UUID) error
// 	GetTransports() ([]*setup.TransportSummary, error)
// }

// ManagementInterface contains the API that is served over RPC for authorized managers
type ManagementInterface struct {
	tpSetup *setup.API
}

// NewManagementInterface returns ManagementInterface
func NewManagementInterface(tpSetup *setup.API) *ManagementInterface {
	m := &ManagementInterface{
		tpSetup: tpSetup,
	}
	return m
}

// // Challenge ...
// func (mi *ManagementInterface) Challenge(in *AddTransportIn, out *setup.TransportSummary) (err error) {

// 	tp, err := r.mgmt.tpSetup.AddTransport(in.RemotePK, in.TpType, in.Timeout)
// 	if tp != nil {
// 		*out = *tp
// 	}

// 	return err
// }
