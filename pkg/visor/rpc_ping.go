// Package visor pkg/visor/rpc.go
package visor

import (
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// DialPing dials to the ping module using the provided pk as a hop.
func (r *RPC) DialPing(conf PingConfig, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DialPing", conf)(nil, &err)

	return r.visor.DialPing(conf)
}

// Ping pings the connected route via DialPing.
func (r *RPC) Ping(conf PingConfig, out *[]time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "Ping", conf)(out, &err)

	*out, err = r.visor.Ping(conf)
	return err
}

// PingOnce performs a single ping on the connected route.
func (r *RPC) PingOnce(conf PingConfig, out *time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "PingOnce", conf)(out, &err)

	*out, err = r.visor.PingOnce(conf)
	return err
}

// StopPing stops the ping conn.
func (r *RPC) StopPing(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopPing", pk)(nil, &err)

	return r.visor.StopPing(*pk)
}

// StopAllPings stops all active ping connections.
func (r *RPC) StopAllPings(_ *struct{}, out *StopAllPingsOut) (err error) {
	defer rpcutil.LogCall(r.log, "StopAllPings", nil)(out, &err)

	count, errs, err := r.visor.StopAllPings()
	out.Stopped = count
	out.Errors = errs
	return err
}

// DialDmsgPing dials to a remote visor over dmsg for ping.
func (r *RPC) DialDmsgPing(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DialDmsgPing", pk)(nil, &err)

	return r.visor.DialDmsgPing(*pk)
}

// DmsgPing pings over dmsg connection.
func (r *RPC) DmsgPing(conf PingConfig, out *[]time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPing", conf)(out, &err)

	*out, err = r.visor.DmsgPing(conf)
	return err
}

// DmsgPingOnce performs a single ping over dmsg connection.
func (r *RPC) DmsgPingOnce(conf PingConfig, out *time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPingOnce", conf)(out, &err)

	*out, err = r.visor.DmsgPingOnce(conf)
	return err
}

// StopDmsgPing stops the dmsg ping conn.
func (r *RPC) StopDmsgPing(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopDmsgPing", pk)(nil, &err)

	return r.visor.StopDmsgPing(*pk)
}

// DialDmsgPingViaServer dials to a remote visor over dmsg via a specific server.
func (r *RPC) DialDmsgPingViaServer(in *DialDmsgPingViaServerIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DialDmsgPingViaServer", in)(nil, &err)

	return r.visor.DialDmsgPingViaServer(in.PK, in.ServerPK)
}

// GetDmsgPingServerPK returns the DMSG server PK used for a ping connection.
func (r *RPC) GetDmsgPingServerPK(pk *cipher.PubKey, out *cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetDmsgPingServerPK", pk)(out, &err)

	*out, err = r.visor.GetDmsgPingServerPK(*pk)
	return err
}

// GetRemoteDmsgServers returns the DMSG servers a remote visor is connected to.
func (r *RPC) GetRemoteDmsgServers(pk *cipher.PubKey, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetRemoteDmsgServers", pk)(out, &err)

	*out, err = r.visor.GetRemoteDmsgServers(*pk)
	return err
}

// GetPreferredDmsgServer returns the lowest-latency DMSG server shared with a remote visor.
func (r *RPC) GetPreferredDmsgServer(remotePK *cipher.PubKey, out *cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetPreferredDmsgServer", remotePK)(out, &err)

	*out, err = r.visor.GetPreferredDmsgServer(*remotePK)
	return err
}
