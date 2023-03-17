// Package manager pkg/manager/api.go
package manager

import (
	"github.com/skycoin/skywire/pkg/transport/setup"
)

type ManagementInterface struct {
	tpSetup *setup.API
}

func NewManagementInterface(tpSetup *setup.API) *ManagementInterface {
	m := &ManagementInterface{
		tpSetup: tpSetup,
	}
	return m
}
