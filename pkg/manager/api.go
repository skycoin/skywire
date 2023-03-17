// Package manager pkg/manager/api.go
package manager

import (
	"github.com/skycoin/skywire/pkg/transport/setup"
)

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
