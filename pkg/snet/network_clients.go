package snet

import (
	"sync"

	"github.com/SkycoinProject/dmsg"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcp"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcph"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcpr"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/sudp"
)

// NetworkClients represents all network clients.
type NetworkClients struct {
	DmsgC *dmsg.Client
	StcpC *stcp.Client
	SudpC *sudp.Client

	stcprCMu      sync.RWMutex
	stcprCReadyCh chan struct{}
	stcprC        *stcpr.Client

	stcphCMu      sync.RWMutex
	stcphCReadyCh chan struct{}
	stcphC        *stcph.Client
}

// StcprC safely gets stcpr client.
func (nc *NetworkClients) StcprC() *stcpr.Client {
	nc.stcprCMu.RLock()
	c := nc.stcprC
	nc.stcprCMu.RUnlock()
	return c
}

// StcphC safely gets stcph client.
func (nc *NetworkClients) StcphC() *stcph.Client {
	nc.stcphCMu.RLock()
	c := nc.stcphC
	nc.stcphCMu.RUnlock()
	return c
}
