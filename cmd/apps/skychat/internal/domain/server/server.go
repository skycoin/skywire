package server

import (
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/pkg/app"
)

// Server defies the chat server
type Server struct {
	appCl     *app.Client                 // Skywire app client //nolint
	clientCh  chan string                 //nolint
	conns     map[cipher.PubKey]peer.Peer // peer connections //nolint
	info      info.Info                   // the public info of the server //nolint
	msgs      []message.Message           // all messages send/received //nolint
	blacklist []cipher.PubKey             // Blacklist to block inocming connections //nolint
	connsMu   sync.Mutex                  //nolint
}
