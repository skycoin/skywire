package chat

import (
	"fmt"
	"reflect"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// Visor defines a remote or the local visor with all its servers
type Visor struct {
	PK cipher.PubKey

	//P2P or direct chat
	P2P Room

	//Server
	Server map[cipher.PubKey]Server
}

// GetPK gets the public key
func (v *Visor) GetPK() cipher.PubKey {
	return v.PK
}

// GetP2P returns peer to peer Room
// if the p2p is empty it returns an empty pointer
func (v *Visor) GetP2P() (*Room, error) {
	return &v.P2P, nil
}

// GetP2PMessages returns the messages from the visors p2p room
func (v *Visor) GetP2PMessages() ([]message.Message, error) {
	p2p, err := v.GetP2P()
	if err != nil {
		return nil, err
	}
	return p2p.GetMessages(), nil
}

// GetRoomMessages returns the messages from the given room route
func (v *Visor) GetRoomMessages(route util.PKRoute) ([]message.Message, error) {
	room, err := v.GetRoomByRoute(route)
	if err != nil {
		return nil, err
	}
	return room.GetMessages(), nil
}

// GetRoomByRoute returns the room from the given route
func (v *Visor) GetRoomByRoute(route util.PKRoute) (*Room, error) {
	server, err := v.GetServerByPK(route.Server)
	if err != nil {
		return nil, err
	}
	return server.GetRoomByPK(route.Room)
}

// P2PIsEmpty returns a bool depending on if the visor has got a p2p
func (v *Visor) P2PIsEmpty() bool {
	return reflect.DeepEqual(v.P2P, Room{})
}

// AddServer adds the given server to the visor
func (v *Visor) AddServer(server Server) error {
	//check if server already exists
	_, err := v.GetServerByPK(server.PKRoute.Server)
	if err != nil {
		//add server to field server
		v.Server[server.PKRoute.Server] = server
		return nil
	}
	return fmt.Errorf("server already exists in visor")
}

// DeleteServer removes the given pk (server) from the visor
func (v *Visor) DeleteServer(pk cipher.PubKey) error {
	//check if server exists
	_, err := v.GetServerByPK(pk)
	if err != nil {
		return fmt.Errorf("server does not exist in visor") //? should this be treaded as an error like now?
	}
	delete(v.Server, pk)
	return nil
}

// SetServer updates the given server
func (v *Visor) SetServer(server Server) error {
	//check if server exists
	_, err := v.GetServerByPK(server.PKRoute.Server)
	if err != nil {
		return fmt.Errorf("server does not exist in visor") //? should this be treaded as an error like now? -> or maybe even call AddServer when server does not exist?
	}
	v.Server[server.PKRoute.Server] = server
	return nil
}

// AddP2P adds the given room as p2p-chat to the visor
func (v *Visor) AddP2P(p2p Room) error {
	//check if p2p already exists
	_, err := v.GetP2P()
	if err != nil {
		return fmt.Errorf("p2p already exists in visor")
	}
	//add server to field server
	v.P2P = p2p
	return nil
}

// SetP2P updates the p2p-chat of the visor
func (v *Visor) SetP2P(p2p Room) error {
	//check if p2p exists
	if !v.P2PIsEmpty() {
		v.P2P = p2p
		return nil
	}
	return fmt.Errorf("setp2p: p2p does not exist in visor") //? should this be treaded as an error like now? -> or maybe even call AddP2P when p2p does not exist?
}

// DeleteP2P removes the p2p-chat-room from the visor
func (v *Visor) DeleteP2P() error {
	//check if p2p exists
	if !v.P2PIsEmpty() {
		v.P2P = Room{}
		return nil
	}
	return fmt.Errorf("deletep2p: p2p does not exist in visor") //? should this be treaded as an error like now?
}

// GetAllServer returns all mapped server
func (v *Visor) GetAllServer() map[cipher.PubKey]Server {
	return v.Server
}

// GetAllServerBoolMap returns a bool-map of all servers
func (v *Visor) GetAllServerBoolMap() map[cipher.PubKey]bool {
	r := make(map[cipher.PubKey]bool)
	for k := range v.Server {
		r[k] = true
	}
	return r
}

// GetServerByPK returns the the server mapped by pk if available and returns err if no server with given pk is available
func (v *Visor) GetServerByPK(pk cipher.PubKey) (*Server, error) {
	if server, ok := v.Server[pk]; ok {
		return &server, nil
	}
	return nil, fmt.Errorf("no server with pk %s found in visor %s", pk.Hex(), v.PK)
}

// AddMessage Adds the given message to the given visor depending on the destination of the message
func (v *Visor) AddMessage(pkroute util.PKRoute, m message.Message) {
	if pkroute.Server == pkroute.Visor {
		v.P2P.AddMessage(m)
		return
	}
	s := v.Server[pkroute.Server]
	s.AddMessage(pkroute, m)
	v.Server[pkroute.Server] = s
}

// SetRouteInfo sets the info of the given route inside the visor
func (v *Visor) SetRouteInfo(pkroute util.PKRoute, info info.Info) error {
	if pkroute.Server == pkroute.Visor {
		p2p, err := v.GetP2P()
		if err != nil {
			return err
		}
		p2p.SetInfo(info)
		err = v.SetP2P(*p2p)
		if err != nil {
			return err
		}
		return nil
	}
	server, err := v.GetServerByPK(pkroute.Server)
	if err != nil {
		return err
	}
	err = server.SetRouteInfo(pkroute, info)
	if err != nil {
		return err
	}

	err = v.SetServer(*server)
	if err != nil {
		return err
	}

	return nil
}

// Constructors

// NewUndefinedVisor creates undefined empty visor to a public key
func NewUndefinedVisor(pk cipher.PubKey) Visor {
	v := Visor{}
	v.PK = pk
	v.Server = make(map[cipher.PubKey]Server)

	return v
}

// NewVisor creates a new visor with p2p and servers
func NewVisor(pk cipher.PubKey, p2p Room, server map[cipher.PubKey]Server) Visor {
	v := Visor{}
	v.PK = pk
	v.P2P = p2p
	v.Server = server
	return v
}

// NewDefaultP2PVisor creates a new visor with only a default p2p room
func NewDefaultP2PVisor(pk cipher.PubKey) Visor {
	v := Visor{}
	v.PK = pk
	v.P2P = NewDefaultP2PRoom(pk)
	v.Server = make(map[cipher.PubKey]Server)

	return v
}

// NewDefaultVisor creates a new default visor
func NewDefaultVisor(route util.PKRoute) Visor {
	v := Visor{}
	v.PK = route.Visor
	v.Server = make(map[cipher.PubKey]Server)

	err := v.AddServer(NewDefaultServer(route))
	if err != nil {
		fmt.Printf("Error in adding server: %s", err)
	}

	return v
}
