// Package cliwisp cmd/skywire-cli/commands/wisp/serve.go c4-vis-cli
//
// `skywire cli wisp serve` — provide a Wisp backend, carrying every stream it
// is asked for over skywire.
//
// LinuxOnTab (linuxontab.com) reads its backend from a query parameter, so no
// change to the page is needed:
//
//	skywire cli wisp serve
//	# then open https://next.linuxontab.com/?wisp=ws://127.0.0.1:6001/wisp
package cliwisp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/wisp"
)

var (
	wispAddr    string
	wispPath    string
	wispSocks   string
	wispDirect  bool
	wispBuffer  uint32
	wispTimeout int
	wispTLSCert string
	wispTLSKey  string
)

func init() {
	serveCmd.Flags().StringVarP(&wispAddr, "addr", "a", "127.0.0.1:6001", "address to serve on")
	serveCmd.Flags().StringVarP(&wispPath, "path", "p", "/wisp", "websocket path")
	serveCmd.Flags().StringVarP(&wispSocks, "socks", "s", "127.0.0.1:1080", "SOCKS5 proxy to carry streams over — the local skysocks-client by default")
	serveCmd.Flags().BoolVar(&wispDirect, "direct", false, "dial the host's own network instead of --socks; nothing is carried over skywire")
	serveCmd.Flags().Uint32VarP(&wispBuffer, "buffer", "b", wisp.DefaultBuffer, "per-stream client→server buffer, in packets")
	serveCmd.Flags().IntVar(&wispTimeout, "dial-timeout", 30, "dial timeout in seconds")
	serveCmd.Flags().StringVar(&wispTLSCert, "tls-cert", "", "TLS certificate — serve wss:// instead of ws://")
	serveCmd.Flags().StringVar(&wispTLSKey, "tls-key", "", "TLS key, with --tls-cert")
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the Wisp protocol over skywire",
	Long: `Serve the Wisp protocol over WebSocket, carrying every stream it is
asked for over skywire via the local SOCKS5 proxy.

Both protocol versions are served. Which one is used is the client's choice: v2
clients open with a Sec-WebSocket-Protocol header and get an INFO exchange
advertising UDP support, v1 clients get the initial CONTINUE straight away.

UDP needs care. SOCKS5 as skysocks implements it has no UDP ASSOCIATE, so a UDP
stream cannot cross an exit as-is. Port 53 is translated to DNS-over-TCP through
the same proxy, which is what lets a guest resolve names without running its own
unbound; every other UDP port is refused rather than quietly leaked to the
clearnet. With --direct, real UDP sockets are used and nothing is refused.

Examples:
  skywire cli wisp serve                             # ws://127.0.0.1:6001/wisp over skywire
  skywire cli wisp serve --addr 0.0.0.0:6001         # serve a LAN, e.g. from a NAS
  skywire cli wisp serve --direct                    # clearnet egress, for comparison
  skywire cli wisp serve --socks 127.0.0.1:9050      # some other SOCKS5 proxy

  # LinuxOnTab reads its backend from a query parameter:
  https://next.linuxontab.com/?wisp=ws://127.0.0.1:6001/wisp`,
	Run: func(cmd *cobra.Command, _ []string) {
		log := logging.MustGetLogger("wisp")

		var (
			egress wisp.Egress
			err    error
		)
		if wispDirect {
			egress = &wisp.DirectEgress{}
		} else {
			egress, err = wisp.NewSocksEgress(wispSocks)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}

		srv, err := wisp.NewServer(wisp.Config{
			Egress:      egress,
			Buffer:      wispBuffer,
			DialTimeout: time.Duration(wispTimeout) * time.Second,
			Log:         log,
		})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		path := wispPath
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}

		mux := http.NewServeMux()
		// Both spellings: LinuxOnTab points at /wisp, while wisp-js
		// refuses any endpoint URL without a trailing slash. Serving
		// only one of them turns the other client's connect into a 404
		// that looks like the server is down.
		mux.Handle(path, srv)
		mux.Handle(path+"/", srv)
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok") //nolint:errcheck,gosec // health probe
		})

		scheme := "ws"
		if wispTLSCert != "" {
			scheme = "wss"
		}

		var b strings.Builder
		fmt.Fprintf(&b, "serving wisp on %s://%s%s\n", scheme, wispAddr, path)
		fmt.Fprintf(&b, "  egress: %s\n", egress.Describe())
		fmt.Fprintf(&b, "  buffer: %d packets per stream\n", wispBuffer)
		internal.PrintOutput(cmd.Flags(), b.String(), b.String())

		httpSrv := &http.Server{
			Addr:              wispAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}

		if wispTLSCert != "" || wispTLSKey != "" {
			if wispTLSCert == "" || wispTLSKey == "" {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--tls-cert and --tls-key must be given together"))
			}
			err = httpSrv.ListenAndServeTLS(wispTLSCert, wispTLSKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
	},
}
