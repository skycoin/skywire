// Package visor pkg/visor/rpc_embedded_services.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// EmbeddedServiceControl stops, starts or restarts an embedded service.
func (r *RPC) EmbeddedServiceControl(in *visorapi.EmbeddedServiceControlArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "EmbeddedServiceControl", in)(nil, &err)
	return r.visor.EmbeddedServiceControl(in.Name, in.Action)
}
