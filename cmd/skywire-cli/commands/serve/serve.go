// Package cliserve cmd/skywire-cli/commands/serve/serve.go
//
// Simple static file server for use with skynet port forwarding.
// Serves a directory over HTTP on localhost, suitable for use as
// the proxy_addr target of a forwarded port.
package cliserve

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var serveAddr string

func init() {
	RootCmd.Flags().StringVarP(&serveAddr, "addr", "a", "127.0.0.1:0", "listen address (default: random port on localhost)")
}

// RootCmd is the serve command.
var RootCmd = &cobra.Command{
	Use:   "serve <directory>",
	Short: "Serve static files over HTTP",
	Long: `Start a local HTTP file server for a directory.

Use with skynet port forwarding to host a website:

  skywire cli util serve /path/to/site
  # prints the listen address, e.g. 127.0.0.1:43210
  # then register it:
  skywire cli skynet port add 80 --proxy-addr 127.0.0.1:43210`,
	Args: cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		dir := args[0]
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", dir)
			os.Exit(1)
		}

		lis, err := net.Listen("tcp", serveAddr)
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
