package appserver

import (
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// ChatAsyncPort is the dmsg-async port skychat 1:1 messages are parked on
// (docs/design/dmsg-async.md, Consumers).
const ChatAsyncPort uint16 = 1

// ChatHost is what a visor does for its skychat app: it parks 1:1 messages
// for offline contacts on their dmsg-async inbox, sealed and signed with the
// visor's key, and opens the ones parked for it.
type ChatHost interface {
	// ChatPark seals msg for rcpt, leaves it on rcpt's inbox hosts and
	// returns the host that took it. ref names the message across retries.
	ChatPark(rcpt cipher.PubKey, msg []byte, ref string) (cipher.PubKey, error)
	// ChatUnpark returns the opened messages parked for this visor, without
	// removing them. Ones that do not open are removed.
	ChatUnpark() ([]ParkedChat, error)
	// ChatAck removes parked messages the app took.
	ChatAck(refs []ParkedRef) error
}

// ParkedChat is a message that waited for this visor on an inbox host. From
// is the key that signed it and At is when the host took it.
type ParkedChat struct {
	ID   string
	Host cipher.PubKey
	From cipher.PubKey
	Msg  []byte
	At   time.Time
}

// ChatParkReq asks the visor to park Msg for Rcpt.
type ChatParkReq struct {
	Rcpt cipher.PubKey
	Msg  []byte
	Ref  string
}

// ErrNoChatHost answers the chat calls of an app the visor does not serve
// them to.
var ErrNoChatHost = errors.New("this visor does not serve chat calls to this app")

// SetChatHostFn wires the visor's ChatHost. fn decides which app may use it.
func (m *procManager) SetChatHostFn(fn func(appName string) ChatHost) {
	m.mx.Lock()
	defer m.mx.Unlock()
	m.chatHostFn = fn
}

// ChatHost returns the ChatHost appName may use, or nil.
func (m *procManager) ChatHost(appName string) ChatHost {
	m.mx.RLock()
	fn := m.chatHostFn
	m.mx.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(appName)
}

func (r *RPCIngressGateway) chatHost() (ChatHost, error) {
	if r.proc == nil || r.proc.m == nil {
		return nil, ErrNoChatHost
	}
	h := r.proc.m.ChatHost(r.proc.appName)
	if h == nil {
		return nil, ErrNoChatHost
	}
	return h, nil
}

// ChatPark parks a message for an offline contact.
func (r *RPCIngressGateway) ChatPark(req *ChatParkReq, resp *cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "ChatPark", nil)(nil, &err)
	h, err := r.chatHost()
	if err != nil {
		return err
	}
	*resp, err = h.ChatPark(req.Rcpt, req.Msg, req.Ref)
	return err
}

// ChatUnpark returns the messages parked for the visor.
func (r *RPCIngressGateway) ChatUnpark(_ *struct{}, resp *[]ParkedChat) (err error) {
	defer rpcutil.LogCall(r.log, "ChatUnpark", nil)(nil, &err)
	h, err := r.chatHost()
	if err != nil {
		return err
	}
	*resp, err = h.ChatUnpark()
	return err
}

// ChatAck removes parked messages the app took.
func (r *RPCIngressGateway) ChatAck(refs *[]ParkedRef, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ChatAck", nil)(nil, &err)
	h, err := r.chatHost()
	if err != nil {
		return err
	}
	return h.ChatAck(*refs)
}

// ChatPark asks the visor to park msg for rcpt.
func (c *rpcIngressClient) ChatPark(rcpt cipher.PubKey, msg []byte, ref string) (cipher.PubKey, error) {
	var resp cipher.PubKey
	err := c.rpc.Call(c.formatMethod("ChatPark"), &ChatParkReq{Rcpt: rcpt, Msg: msg, Ref: ref}, &resp)
	return resp, err
}

// ChatUnpark asks the visor for the messages parked for it.
func (c *rpcIngressClient) ChatUnpark() ([]ParkedChat, error) {
	var resp []ParkedChat
	err := c.rpc.Call(c.formatMethod("ChatUnpark"), &struct{}{}, &resp)
	return resp, err
}

// ChatAck asks the visor to remove parked messages.
func (c *rpcIngressClient) ChatAck(refs []ParkedRef) error {
	return c.rpc.Call(c.formatMethod("ChatAck"), &refs, &struct{}{})
}
