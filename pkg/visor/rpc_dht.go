// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// DHTStatus returns the DHT node's status.
func (r *RPC) DHTStatus(_ *struct{}, out *DHTStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DHTStatus", nil)(out, &err)
	status, err := r.visor.DHTStatus()
	if err != nil {
		return err
	}
	*out = *status
	return nil
}

// DHTGet retrieves a value from the DHT.
func (r *RPC) DHTGet(in *DHTGetIn, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "DHTGet", in)(out, &err)
	data, err := r.visor.DHTGet(in.PK, in.Salt)
	if err != nil {
		return err
	}
	*out = data
	return nil
}

// DHTPut publishes a value to the DHT.
func (r *RPC) DHTPut(in *DHTPutIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DHTPut", in)(nil, &err)
	return r.visor.DHTPut(in.Value, in.Seq, in.Salt)
}

// DHTSetFullNode enables or disables full node mode.
func (r *RPC) DHTSetFullNode(in *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DHTSetFullNode", in)(nil, &err)
	return r.visor.DHTSetFullNode(*in)
}

// DHTGetAll returns all DHT items matching a salt as JSON.
func (r *RPC) DHTGetAll(salt *string, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "DHTGetAll", salt)(out, &err)
	result, err := r.visor.DHTGetAll(*salt)
	if err != nil {
		return err
	}
	*out = result
	return nil
}

// DHTListWithTargets returns all DHT items matching a salt as JSON,
// with each entry annotated with its storage target key.
func (r *RPC) DHTListWithTargets(salt *string, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "DHTListWithTargets", salt)(out, &err)
	result, err := r.visor.DHTListWithTargets(*salt)
	if err != nil {
		return err
	}
	*out = result
	return nil
}

// DHTSync syncs items from a DHT full node.
func (r *RPC) DHTSync(req *DHTSyncRequest, out *int) (err error) {
	defer rpcutil.LogCall(r.log, "DHTSync", req)(out, &err)
	n, err := r.visor.DHTSync(req.RemotePK, req.Salt)
	if err != nil {
		return err
	}
	*out = n
	return nil
}
