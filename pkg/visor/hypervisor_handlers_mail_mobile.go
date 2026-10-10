//go:build mobile

// Package visor pkg/visor/hypervisor_handlers_mail_mobile.go c3-vis-core
// The mobile build has no dashboard, so it serves no mail routes; its app
// reaches the visor's mail over RPC if it needs it.
package visor

import "github.com/skycoin/skywire/pkg/httputil"

func (hv *Hypervisor) mailRoutes(*httputil.Router) {}
