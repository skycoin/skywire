// Package main cmd/hostbridge/main.go c3-vis-wasm
//
// Command hostbridge lends a browser visor the host's sockets. It is the
// same as `skywire hostbridge`, built alone so it stays small enough to
// ship inside the wasm visor.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/skycoin/skywire/pkg/hostbridge"
)

func main() {
	addr := flag.String("addr", hostbridge.DefaultAddr, "address the tab connects to")
	origin := flag.String("origin", hostbridge.DefaultOrigin, "comma separated pages allowed to connect, * for any")
	token := flag.String("token", "", "secret the tab must present, read from -token-file when empty")
	tokenFile := flag.String("token-file", "", "file that keeps the token across restarts, default <user config dir>/skywire/hostbridge-token, none for a new token each run")
	quiet := flag.Bool("q", false, "log only the connect URL")
	flag.Parse()

	t, err := hostbridge.ResolveToken(*token, *tokenFile)
	if err != nil {
		_, _ = os.Stderr.WriteString("hostbridge: " + err.Error() + "\n") //nolint:errcheck
		os.Exit(1)
	}
	cfg := hostbridge.Config{Token: t, Origins: hostbridge.SplitOrigins(*origin)}
	if !*quiet {
		cfg.Logf = log.Printf
	}
	err = hostbridge.Run(*addr, cfg, func(url string) {
		_, _ = os.Stderr.WriteString("connect the visor to " + url + "\n") //nolint:errcheck
	})
	if err != nil {
		_, _ = os.Stderr.WriteString("hostbridge: " + err.Error() + "\n") //nolint:errcheck
		os.Exit(1)
	}
}
