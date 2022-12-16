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
// .\skywire-visor.exe -c .\skywire-config.json

/*

Text message
{"Id":0,"Origin":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Time":"2022-07-02T00:06:33.0067605+02:00","Sender":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Msgtype":2,"MsgSubtype":0,"Message":"Hey Friend","Status":1,"Seen":false}

Accept message
{"Id":0,"Origin":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Time":"2022-07-02T00:06:33.0067605+02:00","Sender":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Msgtype":1,"MsgSubtype":2,"Message":"","Status":1,"Seen":false}

Request message
{"Id":0,"Origin":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Time":"2022-07-02T00:06:33.0067605+02:00","Sender":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Msgtype":1,"MsgSubtype":1,"Message":"","Status":1,"Seen":false}

{"Id":0,"Origin":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Time":"2022-07-02T14:01:02.3116213+02:00","Sender":"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22","Msgtype":3,"MsgSubtype":0,"Message":"{\"Pk\":\"03389acab4f1a39ebd6e5547acf733b99415f69983270de500715dfef56cddda22\",\"Alias\":\"Meister\",\"Desc\":\"Unknown\",\"Img\":\""}","Status":1,"Seen":false}
*/
