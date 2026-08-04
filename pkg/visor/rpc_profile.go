// Package visor pkg/visor/rpc_profile.go c3-vis-core
//
// RPC adapter for skychat profiles. Thin wrappers over the Visor methods
// in profile.go. Mirrors rpc_group.go shape.
package visor

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// ProfileGet returns what this visor publishes about itself.
func (r *RPC) ProfileGet(_ *struct{}, out *Profile) (err error) {
	defer rpcutil.LogCall(r.log, "ProfileGet", nil)(out, &err)
	p, err := r.visor.ProfileGet()
	if err != nil {
		return err
	}
	*out = p
	return nil
}

// ProfileSet writes this visor's published profile and returns the stored
// result.
func (r *RPC) ProfileSet(req *ProfileSetArgs, out *Profile) (err error) {
	defer rpcutil.LogCall(r.log, "ProfileSet", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	p, err := r.visor.ProfileSet(*req)
	if err != nil {
		return err
	}
	*out = p
	return nil
}

// ProfileFetch asks the visor at pk who it is. A zero key means this visor.
func (r *RPC) ProfileFetch(pk *cipher.PubKey, out *Profile) (err error) {
	defer rpcutil.LogCall(r.log, "ProfileFetch", pk)(out, &err)
	if pk == nil {
		return fmt.Errorf("nil request")
	}
	p, err := r.visor.ProfileFetch(*pk)
	if err != nil {
		return err
	}
	*out = p
	return nil
}
