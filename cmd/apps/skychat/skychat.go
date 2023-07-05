// /* cmd/apps/skychat/skychat.go
/*
skychat app for skywire visor
*/
package main

import "github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/cli"

func main() {
	//cobra-cli
	cli.Execute()

}

//TEST:
// cd ..
// cd ..
// cd ..
// mingw32-make build-windows
// .\skywire-cli.exe config gen -i -r -o .\skywire-config.json
// .\build\skywire-visor.exe -c .\skywire-config.json --loglvl debug
