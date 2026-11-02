// Package visor pkg/visor/rpc_dmsg_async.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// DmsgAsyncStatus reports the dmsg-async host and inbox. See Visor.DmsgAsyncStatus.
func (r *RPC) DmsgAsyncStatus(_ *struct{}, out *visorapi.DmsgAsyncStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgAsyncStatus", nil)(nil, &err)
	st, err := r.visor.DmsgAsyncStatus()
	if err != nil {
		return err
	}
	*out = *st
	return nil
}

// DmsgAsyncSend deposits a payload for an offline key. See Visor.DmsgAsyncSend.
func (r *RPC) DmsgAsyncSend(in *visorapi.DmsgAsyncSendRequest, out *visorapi.DmsgAsyncSendResult) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgAsyncSend", nil)(nil, &err)
	res, err := r.visor.DmsgAsyncSend(*in)
	if err != nil {
		return err
	}
	*out = *res
	return nil
}

// DmsgAsyncTake fetches and acks waiting messages. See Visor.DmsgAsyncTake.
func (r *RPC) DmsgAsyncTake(ports *[]uint16, out *[]dmsgasync.Message) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgAsyncTake", ports)(nil, &err)
	*out, err = r.visor.DmsgAsyncTake(*ports)
	return err
}

// DmsgAsyncSync registers, publishes and fetches now. See Visor.DmsgAsyncSync.
func (r *RPC) DmsgAsyncSync(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgAsyncSync", nil)(nil, &err)
	return r.visor.DmsgAsyncSync()
}
