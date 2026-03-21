package node

import (
	"errors"
	"net"
	"net/rpc"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// wrap the RPC
type rpcServer struct {
	l net.Listener // underlying listener
	r *rpc.Server  //
	n *Node        // back reference
}

// create RPC server
func (n *Node) newRPC() (r *rpcServer) {
	r = new(rpcServer)
	r.n = n
	r.r = rpc.NewServer()
	return
}

func (r *rpcServer) Listen(address string) (err error) {

	r.r.RegisterName("node", &RPC{r.n}) //nolint:errcheck,gosec

	r.r.RegisterName("tcp", &TCPRPC{r.n}) //nolint:errcheck,gosec
	r.r.RegisterName("udp", &UDPRPC{r.n}) //nolint:errcheck,gosec

	r.r.RegisterName("root", &RootRPC{r.n}) //nolint:errcheck,gosec

	if r.l, err = net.Listen("tcp", address); err != nil {
		return
	}

	r.n.await.Add(1)
	go r.run()

	return
}

func (r *rpcServer) run() {
	defer r.n.await.Done()
	r.r.Accept(r.l)
}

func (r *rpcServer) Address() (address string) {
	if r.l != nil {
		address = r.l.Addr().String()
	}
	return
}

func (r *rpcServer) Close() (err error) {
	if r.l != nil {
		err = r.l.Close() //nolint:errcheck,gosec
	}
	return
}

// A RPC represnet RPC server of the Node.
// The RPC is exported because the net/rpc
// package requires it. E.g. the RPC is
// registered by the rpc package. The RPC
// can't be used outside the Node.
//
// Short words, the RPC is internal
type RPC struct {
	n *Node // back reference
}

// Share is RPC method
func (r *RPC) Share(pk cipher.PubKey, _ *struct{}) (err error) {
	return r.n.Share(pk)
}

// DontShare is RPC method
func (r *RPC) DontShare(pk cipher.PubKey, _ *struct{}) (err error) {
	return r.n.DontShare(pk)
}

// Feeds is RPC method
func (r *RPC) Feeds(_ struct{}, fs *[]cipher.PubKey) (_ error) {
	*fs = r.n.Feeds()
	return
}

// IsSharing is RPC method
func (r *RPC) IsSharing(pk cipher.PubKey, yep *bool) (_ error) {
	*yep = r.n.IsSharing(pk)
	return
}

// strings with all connections
func (n *Node) connections() (cs []string) {
	n.mx.Lock()
	defer n.mx.Unlock()

	cs = make([]string, 0, len(n.pendConns)+len(n.addrToConn))

	for _, c := range n.pendConns {
		cs = append(cs, c.String()+"(⌛)") // pending
	}

	for _, c := range n.addrToConn {
		cs = append(cs, c.String()+"(✓)") // established
	}

	return
}

// Connections is RPC method
func (r *RPC) Connections(_ struct{}, cs *[]string) (_ error) {
	*cs = r.n.connections()
	return
}

// ConnectionsOfFeed is RPC method
func (r *RPC) ConnectionsOfFeed(feed cipher.PubKey, cs *[]string) (_ error) {
	var cf = r.n.ConnectionsOfFeed(feed)

	if len(cf) == 0 {
		*cs = []string{}
		return
	}

	var lcs = make([]string, 0, len(cf))

	for _, c := range cf {
		lcs = append(lcs, c.String())
	}

	*cs = lcs
	return
}

// Config is RPC method
func (r *RPC) Config(_ struct{}, config *Config) (err error) {
	*config = *r.n.config // copy
	return

}

// Stat is RPC method
func (r *RPC) Stat(_ struct{}, stat *Stat) (err error) {
	*stat = *r.n.Stat()
	return
}

// A TCPRPC represents RPC object
// of TCP transport of the Node
type TCPRPC struct {
	n *Node
}

// Connect is RPC method
func (t *TCPRPC) Connect(address string, _ *struct{}) (err error) {
	_, err = t.n.TCP().Connect(address)
	return
}

// Disconnect is RPC method
func (t *TCPRPC) Disconnect(address string, _ *struct{}) (err error) {
	if tcp := t.n.getTCP(); tcp != nil {
		if c := tcp.getConn(address); c != nil {
			err = c.Close() //nolint:errcheck,gosec
		}
	}
	return
}

// A ConnFeed represents connection address and feed
type ConnFeed struct {
	Address string
	Feed    cipher.PubKey
}

// Subscribe is RPC method
func (t *TCPRPC) Subscribe(cf ConnFeed, _ *struct{}) (err error) {
	if tcp := t.n.getTCP(); tcp != nil {
		if c := tcp.getConn(cf.Address); c != nil {
			return c.Subscribe(cf.Feed)
		}
		return errors.New("no such connection")
	}
	return errors.New("to TCP transport")
}

// Unsubscribe is RPC method
func (t *TCPRPC) Unsubscribe(cf ConnFeed, _ *struct{}) (err error) {
	if tcp := t.n.getTCP(); tcp != nil {
		if c := tcp.getConn(cf.Address); c != nil {
			c.Unsubscribe(cf.Feed)
			return
		}
		return errors.New("no such connection")
	}
	return errors.New("to TCP transport")
}

// RemoteFeeds is RPC method
func (t *TCPRPC) RemoteFeeds(address string, rfs *[]cipher.PubKey) (err error) {
	if tcp := t.n.getTCP(); tcp != nil {
		if c := tcp.getConn(address); c != nil {
			var rf []cipher.PubKey
			if rf, err = c.RemoteFeeds(); err != nil {
				return
			}
			*rfs = rf
			return // nil
		}
		return errors.New("no such connection")
	}
	return errors.New("to TCP transport")
}

// Address is RPC method
func (t *TCPRPC) Address(_ struct{}, address *string) (_ error) {
	if tcp := t.n.getTCP(); tcp != nil {
		*address = tcp.Address()
		return
	}
	return errors.New("to TCP transport")
}

// A UDPRPC represents RPC object
// of UDP transport of the Node
type UDPRPC struct {
	n *Node
}

// Connect is RPC method
func (u *UDPRPC) Connect(address string, _ *struct{}) (err error) {
	_, err = u.n.TCP().Connect(address)
	return
}

// Disconnect is RPC method
func (u *UDPRPC) Disconnect(address string, _ *struct{}) (err error) {
	if tcp := u.n.getTCP(); tcp != nil {
		if c := tcp.getConn(address); c != nil {
			err = c.Close() //nolint:errcheck,gosec
		}
	}
	return
}

// Subscribe is RPC method
func (u *UDPRPC) Subscribe(cf ConnFeed, _ *struct{}) (err error) {
	if tcp := u.n.getTCP(); tcp != nil {
		if c := tcp.getConn(cf.Address); c != nil {
			return c.Subscribe(cf.Feed)
		}
		return errors.New("no such connection")
	}
	return errors.New("to UDP transport")
}

// Unsubscribe is RPC method
func (u *UDPRPC) Unsubscribe(cf ConnFeed, _ *struct{}) (err error) {
	if tcp := u.n.getTCP(); tcp != nil {
		if c := tcp.getConn(cf.Address); c != nil {
			c.Unsubscribe(cf.Feed)
			return
		}
		return errors.New("no such connection")
	}
	return errors.New("to UDP transport")
}

// RemoteFeeds is RPC method
func (u *UDPRPC) RemoteFeeds(address string, rfs *[]cipher.PubKey) (err error) {
	if tcp := u.n.getTCP(); tcp != nil {
		if c := tcp.getConn(address); c != nil {
			var rf []cipher.PubKey
			if rf, err = c.RemoteFeeds(); err != nil {
				return
			}
			*rfs = rf
			return // nil
		}
		return errors.New("no such connection")
	}
	return errors.New("to UDP transport")
}

// Address is RPC method
func (u *UDPRPC) Address(_ struct{}, address *string) (_ error) {
	if tcp := u.n.getTCP(); tcp != nil {
		*address = tcp.Address()
		return
	}
	return errors.New("to UDP transport")
}

// A RootRPC represents RPC object
// of Root objects of the Node
type RootRPC struct {
	n *Node
}

// A RootSelector represents Root selector
type RootSelector struct {
	Feed  cipher.PubKey
	Nonce uint64
	Seq   uint64
}

// Show Root (RPC method)
func (r *RootRPC) Show(rs RootSelector, z *registry.Root) (err error) {
	var x *registry.Root
	if x, err = r.n.c.Root(rs.Feed, rs.Nonce, rs.Seq); err != nil {
		return
	}

	*z = *x
	return
}

// Tree of Root (RPC method)
func (r *RootRPC) Tree(rs RootSelector, tree *string) (err error) {

	var x *registry.Root
	if x, err = r.n.c.Root(rs.Feed, rs.Nonce, rs.Seq); err != nil {
		return
	}

	var p registry.Pack
	if p, err = r.n.c.Pack(x, nil); err != nil {
		return
	}

	var t string
	t, err = x.Tree(p)
	*tree = t
	return
}

// Last Root of given Feed (RPC method)
func (r *RootRPC) Last(feed cipher.PubKey, z *registry.Root) (err error) {
	var x *registry.Root
	if x, err = r.n.c.LastRoot(feed, r.n.c.ActiveHead(feed)); err != nil {
		return
	}
	*z = *x
	return
}
