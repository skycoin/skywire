// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// CheckAREntry checks if a PK is registered in the address resolver.
func (r *RPC) CheckAREntry(pk *string, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "CheckAREntry", pk)(out, &err)
	result, err := r.visor.CheckAREntry(*pk)
	if err != nil {
		return err
	}
	*out = result
	return nil
}

// ARSelfInfo returns the visor's own AR registration (STCPR + SUDPH).
func (r *RPC) ARSelfInfo(_ *struct{}, out *ARSelfRegistration) (err error) {
	defer rpcutil.LogCall(r.log, "ARSelfInfo", nil)(out, &err)
	reg, err := r.visor.ARSelfInfo()
	if err != nil {
		return err
	}
	*out = *reg
	return nil
}
