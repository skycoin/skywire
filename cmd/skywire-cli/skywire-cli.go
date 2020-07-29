package main

import (
	"github.com/SkycoinProject/skywire-mainnet/pkg/skywiremob"
)

func main() {
	skywiremob.PrepareLogger()
	skywiremob.PrepareVisor()
	skywiremob.PrintDmsgServers()
	//commands.Execute()
}
