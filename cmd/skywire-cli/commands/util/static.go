// Package cliutil cmd/skywire-cli/commands/util/static.go
//
// `skywire cli util serve` — a small static-file HTTP server for
// pairing with `cli serve add 80 --to <addr>`. Lives under `util`
// because it isn't part of the core visor RPC surface; the visor
// itself doesn't host static files.
//
// Was top-level `cli serve <dir>` historically; the top-level name
// was reclaimed for port registration. Old invocations should use
// `cli util serve <dir>`.
package cliutil

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var staticServeAddr string

func init() {
	staticServeCmd.Flags().StringVarP(&staticServeAddr, "addr", "a", "127.0.0.1:0", "listen address (default: random port on localhost)")
}

var staticServeCmd = &cobra.Command{
	Use:   "serve <directory>",
	Short: "Serve static files over HTTP",
	Long: `Start a local HTTP file server for a directory.

Use with skywire port forwarding to host a website:

  skywire cli util serve /path/to/site
  # prints the listen address, e.g. 127.0.0.1:43210
  # then register it:
  skywire cli serve add 80 --to 127.0.0.1:43210`,
	Args: cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		dir := args[0]
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", dir)
			os.Exit(1)
		}

		lis, err := net.Listen("tcp", staticServeAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Serving %s on http://%s\n", dir, lis.Addr())

		if err := http.Serve(lis, http.FileServer(http.Dir(dir))); err != nil { //nolint:gosec
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}
