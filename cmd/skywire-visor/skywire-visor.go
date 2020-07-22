/*
skywire visor
*/
package main

import (
	"fmt"
	"net"

	"github.com/SkycoinProject/skywire-mainnet/cmd/skywire-visor/commands"
)

func main() {
	ips, err := net.LookupIP("address.resolver.skywire.cc")
	if err != nil {
		panic(err)
	}
	for _, ip := range ips {
		fmt.Printf("IP: %v\n", ip.String())
	}
	commands.Execute()
}
