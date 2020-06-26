package snet

import (
	"sync"

	"github.com/SkycoinProject/dmsg"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcp"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/sudp"
)

// NetworkClients represents all network clients.
type NetworkClients struct {
	DmsgC *dmsg.Client
	StcpC *stcp.Client
	SudpC *sudp.Client

	stcprCMu      sync.RWMutex
	stcprCReadyCh chan struct{}
	stcprC        directtransport.Client

	stcphCMu      sync.RWMutex
	stcphCReadyCh chan struct{}
	stcphC        directtransport.Client

	sudprCMu      sync.RWMutex
	sudprCReadyCh chan struct{}
	sudprC        directtransport.Client

	sudphCMu      sync.RWMutex
	sudphCReadyCh chan struct{}
	sudphC        directtransport.Client
}

// StcprC safely gets stcpr client.
func (nc *NetworkClients) StcprC() directtransport.Client {
	nc.stcprCMu.RLock()
	c := nc.stcprC
	nc.stcprCMu.RUnlock()
	return c
}

// StcphC safely gets stcph client.
func (nc *NetworkClients) StcphC() directtransport.Client {
	nc.stcphCMu.RLock()
	c := nc.stcphC
	nc.stcphCMu.RUnlock()
	return c
}

// SudprC safely gets sudpr client.
func (nc *NetworkClients) SudprC() directtransport.Client {
	nc.sudprCMu.RLock()
	c := nc.sudprC
	nc.sudprCMu.RUnlock()
	return c
}

// SudphC safely gets sudph client.
func (nc *NetworkClients) SudphC() directtransport.Client {
	nc.sudphCMu.RLock()
	c := nc.sudphC
	nc.sudphCMu.RUnlock()
	return c
}
