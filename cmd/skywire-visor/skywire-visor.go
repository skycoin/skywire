/*
skywire visor
*/
package main

import (
	"embed"

	"github.com/skycoin/skywire/cmd/skywire-visor/commands"
)

//go:embed static
var uiAssets embed.FS

func main() {
	commands.Execute(uiAssets)
}
