package server

import (
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/pkg/app"
)

type Server struct {
	appCl     *app.Client                 // Skywire app client
	clientCh  chan string                 //
	conns     map[cipher.PubKey]peer.Peer // peer connections
	info      info.Info                   // the public info of the server
	msgs      []message.Message           // all messages send/received
	blacklist []cipher.PubKey             // Blacklist to block inocming connections
	connsMu   sync.Mutex                  //
}
