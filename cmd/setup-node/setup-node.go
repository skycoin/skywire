package main

import (
	"log"

	"github.com/SkycoinProject/skywire-mainnet/cmd/setup-node/commands"
	"github.com/SkycoinProject/skywire-mainnet/pkg/buildinfo"
)

func main() {
	if _, err := buildinfo.WriteTo(log.Writer()); err != nil {
		log.Printf("Failed to output build info: %v", err)
	}

	commands.Execute()
}
