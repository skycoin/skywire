package node

import (
	"github.com/skycoin/skywire/pkg/cxo/node/log"
)

// debug log pins
const (

	// connections

	NewInConnPin  log.Pin = 1 << iota // new incoming connection
	NewOutConnPin                     // new outgoing connection

	ConnHskPin   // handshake logs
	ConnEstPin   // connection established
	CloseConnPin // connection closed (except ERR logs)

	// messeges

	MsgSendPin    // send msg
	MsgReceivePin // receive msg

	// filling

	FillPin //

	// feeds

	FeedPin //

	// discovery

	DiscoveryPin // show discovery debug logs

	// peer exchange

	PEXPin // show peer exchange debug logs

	// joiners

	MsgPin  = MsgSendPin | MsgReceivePin // send/receive
	ConnPin = NewInConnPin | NewOutConnPin | ConnEstPin | ConnHskPin |
		CloseConnPin // connections
)
