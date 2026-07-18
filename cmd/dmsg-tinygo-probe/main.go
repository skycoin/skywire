// Package main cmd/dmsg-tinygo-probe/main.go c1-net-dmsg
// Command dmsg-tinygo-probe is a minimal build-check that the dmsg client core
// compiles under TinyGo (IoT / embedded targets). It constructs a dmsg.Client
// and exits — it does not dial (a real IoT build supplies its own transport and
// discovery). Its purpose is to keep the TinyGo build green in CI:
//
//	tinygo build -target wasip1 -o /dev/null ./cmd/dmsg-tinygo-probe
//
// (see the `tinygo-dmsg` Makefile target). It also builds and runs as a normal
// Go program. See docs/design/tinygo-dmsg-client.md for the port.
package main

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func main() {
	pk, sk := cipher.GenerateKeyPair()
	var dc disc.APIClient // a real IoT build supplies a dmsg-only disc client
	c := dmsg.NewClient(pk, sk, dc, dmsg.DefaultConfig())
	_ = c
}
