// Package visor pkg/visor/rpc.go
package visor

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// TransportTypes lists all transport types supported by the Visor.
func (r *RPC) TransportTypes(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "TransportTypes", nil)(out, &err)

	types, err := r.visor.TransportTypes()
	*out = types

	return err
}

// Transports lists Transports of the Visor and provides a summary of each.
func (r *RPC) Transports(in *TransportsIn, out *[]*TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transports", in)(out, &err)

	transports, err := r.visor.Transports(in.FilterTypes, in.FilterPubKeys, in.ShowLogs)
	*out = transports

	return err
}

// Transport obtains a Transport Summary of Transport of given Transport ID.
func (r *RPC) Transport(in *uuid.UUID, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transport", in)(out, &err)

	tp, err := r.visor.Transport(*in)
	if tp != nil {
		*out = *tp
	}

	return err
}

// AddTransport creates a transport for the visor.
func (r *RPC) AddTransport(in *AddTransportIn, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "AddTransport", in)(out, &err)

	tp, err := r.visor.AddTransport(in.RemotePK, in.TpType, in.Timeout, in.Label, in.NoRegister, in.SkipLatencyProbe)
	if tp != nil {
		*out = *tp
	}

	return err
}

// SetSTCPAddr injects an STCP PK table entry at runtime.
func (r *RPC) SetSTCPAddr(in *SetSTCPAddrIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetSTCPAddr", in)(nil, &err)
	return r.visor.SetSTCPAddr(in.PK, in.Addr)
}

// RemoveTransport removes a Transport from the visor.
func (r *RPC) RemoveTransport(tid *uuid.UUID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveTransport", tid)(nil, &err)

	return r.visor.RemoveTransport(*tid)
}

// RemoveAllTransports removes all Transports from the visor.
func (r *RPC) RemoveAllTransports(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveAllTransports", nil)(nil, &err)

	return r.visor.RemoveAllTransports()
}

// DiscoverTransportsByPK obtains available transports via the transport discovery via given public key.
func (r *RPC) DiscoverTransportsByPK(pk *cipher.PubKey, out *[]*transport.Entry) (err error) {
	defer rpcutil.LogCall(r.log, "DiscoverTransportsByPK", pk)(out, &err)

	entries, err := r.visor.DiscoverTransportsByPK(*pk)
	*out = entries

	return err
}

// DiscoverTransportByID obtains available transports via the transport discovery via a given transport ID.
func (r *RPC) DiscoverTransportByID(id *uuid.UUID, out *transport.Entry) (err error) {
	defer rpcutil.LogCall(r.log, "DiscoverTransportByID", id)(out, &err)

	entry, err := r.visor.DiscoverTransportByID(*id)
	if entry != nil {
		*out = *entry
	}

	return err
}

// GetPersistentTransports gets persistent_transports from visor's routing config
func (r *RPC) GetPersistentTransports(_ *struct{}, out *[]transport.PersistentTransports) (err error) {
	defer rpcutil.LogCall(r.log, "GetPersistentTransports", nil)(out, &err)

	pTs, err := r.visor.GetPersistentTransports()
	*out = pTs
	return err
}

// SetPersistentTransports sets persistent_transports in visor's routing config
func (r *RPC) SetPersistentTransports(pTs *[]transport.PersistentTransports, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetPersistentTransports", *pTs)(nil, &err)
	err = r.visor.SetPersistentTransports(*pTs)
	return err
}

// GetTransportLogs returns transport log entries from the last N days.
func (r *RPC) GetTransportLogs(days *int, out *[]TransportLogEntry) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportLogs", *days)(out, &err)
	entries, err := r.visor.GetTransportLogs(*days)
	if err != nil {
		return err
	}
	*out = entries
	return nil
}

// SetPublicAutoconnect sets public_autoconnect in visor's routing config
func (r *RPC) SetPublicAutoconnect(pAc *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetPublicAutoconnect", *pAc)(nil, &err)
	err = r.visor.SetPublicAutoconnect(*pAc)
	return err
}

// SetIsPublic sets the is_public field in the visor config and flushes.
func (r *RPC) SetIsPublic(isPublic *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetIsPublic", *isPublic)(nil, &err)
	return r.visor.SetIsPublic(*isPublic)
}

// GetIsPublic returns whether the visor is configured as public.
func (r *RPC) GetIsPublic(_ *struct{}, out *bool) (err error) {
	*out = r.visor.GetIsPublic()
	return nil
}

// StartPublicAutoconnect starts the public autoconnect routine
func (r *RPC) StartPublicAutoconnect(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartPublicAutoconnect", nil)(nil, &err)
	return r.visor.StartPublicAutoconnect()
}

// StopPublicAutoconnect stops the public autoconnect routine
func (r *RPC) StopPublicAutoconnect(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopPublicAutoconnect", nil)(nil, &err)
	return r.visor.StopPublicAutoconnect()
}

// PublicAutoconnectStatus returns whether public autoconnect is running
func (r *RPC) PublicAutoconnectStatus(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "PublicAutoconnectStatus", nil)(out, &err)
	status, err := r.visor.PublicAutoconnectStatus()
	*out = status
	return err
}

// SetExistingTPOnly sets whether to only use existing transports for routing
func (r *RPC) SetExistingTPOnly(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetExistingTPOnly", *enabled)(nil, &err)
	err = r.visor.SetExistingTPOnly(*enabled)
	return err
}

// TransportRPCCall dials a remote visor's transport RPC and calls the
// specified method. The local visor acts as a proxy — it opens a VStream
// over an existing transport to the remote visor and forwards the RPC.
// The remote visor must have this visor's PK in its hypervisor/dmsgpty whitelist.
func (r *RPC) TransportRPCCall(req *TransportRPCCallRequest, out *json.RawMessage) (err error) {
	defer rpcutil.LogCall(r.log, "TransportRPCCall", req)(out, &err)

	v, ok := r.visor.(*Visor)
	if !ok || v.tpM == nil {
		return fmt.Errorf("transport manager not available")
	}

	log := logging.MustGetLogger("transport_rpc_proxy")
	rpcC, dialErr := DialTransportRPC(req.RemotePK, v.tpM, log)
	if dialErr != nil {
		return dialErr
	}
	defer rpcC.Close() //nolint:errcheck,gosec

	// Call the remote method, get raw JSON result.
	var rpcArgs interface{} = &struct{}{}
	if len(req.Args) > 0 {
		rpcArgs = &req.Args
	}
	var result json.RawMessage
	if callErr := rpcC.Call(req.Method, rpcArgs, &result); callErr != nil {
		return fmt.Errorf("remote RPC %s: %w", req.Method, callErr)
	}
	*out = result
	return nil
}
