package appserver

import (
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// MailHost is what a visor does for its mailbox app. The visor keeps its
// secret key, so the app asks it to seal and open envelopes.
type MailHost interface {
	MailSeal(msg []byte, rcpt cipher.PubKey) ([]byte, error)
	MailOpen(body []byte) ([]byte, cipher.PubKey, error)
	MailHostInfo() MailHostInfo
}

// MailHostInfo is the visor state a mailbox app follows.
type MailHostInfo struct {
	// Managed are the visors connected to this visor's hypervisor, the
	// default helpers. Hypervisors are this visor's own, whose sealed
	// mail it carries by default.
	Managed     []cipher.PubKey
	Hypervisors []cipher.PubKey
	// PortServed is set when `serve add 25` gives port 25 to the host's
	// own mail server, which then receives the mail instead.
	PortServed bool
}

// MailSealReq asks the visor to seal Msg for Rcpt.
type MailSealReq struct {
	Msg  []byte
	Rcpt cipher.PubKey
}

// MailOpenResp is an opened envelope and the PK that signed it.
type MailOpenResp struct {
	Msg    []byte
	Signer cipher.PubKey
}

// ErrNoMailHost answers the mail calls of an app the visor does not
// serve them to.
var ErrNoMailHost = errors.New("this visor does not serve mail calls to this app")

// SetMailHostFn wires the visor's MailHost. fn decides which app may use it.
func (m *procManager) SetMailHostFn(fn func(appName string) MailHost) {
	m.mx.Lock()
	defer m.mx.Unlock()
	m.mailHostFn = fn
}

// MailHost returns the MailHost appName may use, or nil.
func (m *procManager) MailHost(appName string) MailHost {
	m.mx.RLock()
	fn := m.mailHostFn
	m.mx.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(appName)
}

func (r *RPCIngressGateway) mailHost() (MailHost, error) {
	if r.proc == nil || r.proc.m == nil {
		return nil, ErrNoMailHost
	}
	h := r.proc.m.MailHost(r.proc.appName)
	if h == nil {
		return nil, ErrNoMailHost
	}
	return h, nil
}

// MailSeal seals a message as the visor for one recipient.
func (r *RPCIngressGateway) MailSeal(req *MailSealReq, resp *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "MailSeal", nil)(nil, &err)
	h, err := r.mailHost()
	if err != nil {
		return err
	}
	*resp, err = h.MailSeal(req.Msg, req.Rcpt)
	return err
}

// MailOpen opens an envelope sealed to the visor.
func (r *RPCIngressGateway) MailOpen(body *[]byte, resp *MailOpenResp) (err error) {
	defer rpcutil.LogCall(r.log, "MailOpen", nil)(nil, &err)
	h, err := r.mailHost()
	if err != nil {
		return err
	}
	resp.Msg, resp.Signer, err = h.MailOpen(*body)
	return err
}

// MailHostInfo reports the visor state a mailbox app follows.
func (r *RPCIngressGateway) MailHostInfo(_ *struct{}, resp *MailHostInfo) (err error) {
	defer rpcutil.LogCall(r.log, "MailHostInfo", nil)(nil, &err)
	h, err := r.mailHost()
	if err != nil {
		return err
	}
	*resp = h.MailHostInfo()
	return nil
}

// MailSeal asks the visor to seal msg for rcpt.
func (c *rpcIngressClient) MailSeal(msg []byte, rcpt cipher.PubKey) ([]byte, error) {
	var resp []byte
	err := c.rpc.Call(c.formatMethod("MailSeal"), &MailSealReq{Msg: msg, Rcpt: rcpt}, &resp)
	return resp, err
}

// MailOpen asks the visor to open an envelope sealed to it.
func (c *rpcIngressClient) MailOpen(body []byte) ([]byte, cipher.PubKey, error) {
	var resp MailOpenResp
	if err := c.rpc.Call(c.formatMethod("MailOpen"), &body, &resp); err != nil {
		return nil, cipher.PubKey{}, err
	}
	return resp.Msg, resp.Signer, nil
}

// MailHostInfo asks the visor for the state a mailbox app follows.
func (c *rpcIngressClient) MailHostInfo() (MailHostInfo, error) {
	var resp MailHostInfo
	err := c.rpc.Call(c.formatMethod("MailHostInfo"), &struct{}{}, &resp)
	return resp, err
}
