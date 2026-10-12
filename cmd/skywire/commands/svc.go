//go:build !js && !tinygo

package commands

import (
	services "github.com/skycoin/skywire/cmd/svc/skywire-services/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

// The deployment services need Redis, Postgres and listening sockets, so the
// js/wasm build leaves them out. They were about 14 MB of the browser module.
func init() {
	flags.InitStyle(services.RootCmd)
	services.RootCmd.Use = "svc"
	RootCmd.AddCommand(services.RootCmd)
}
