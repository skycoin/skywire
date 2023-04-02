// /* cmd/apps/skychat/skychat.go
/*
skychat app for skywire visor
*/
package main

import (
	"flag"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters"
)

var addr = flag.String("addr", ":8001", "address to bind")

func main() {

	flag.Parse()

	interfaceAdapterServices := interfaceadapters.NewServices()
	defer interfaceAdapterServices.Close()
	appServices := app.NewServices(interfaceAdapterServices.ClientRepository, interfaceAdapterServices.UserRepository, interfaceAdapterServices.VisorRepository, interfaceAdapterServices.NotificationService, interfaceAdapterServices.MessengerService)
	inputPortsServices := inputports.NewServices(appServices)

	//appclient listen
	go interfaceAdapterServices.MessengerService.Listen()

	//http-server
	inputPortsServices.Server.ListenAndServe(addr)

}

//TEST:
// cd ..
// cd ..
// cd ..
// mingw32-make build-windows
// .\skywire-cli.exe config gen -i -r -o .\skywire-config.json
// .\skywire-visor.exe -c .\skywire-config.json --loglvl debug
