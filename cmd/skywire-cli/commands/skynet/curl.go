// Package skynet curl.go — HTTP requests over skynet
package skynet

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	curlData    string
	curlOutput  string
	curlVerbose bool
	curlVLevel  string
)

func init() {
	curlCmd.Flags().StringVarP(&curlData, "data", "d", "", "HTTP POST data")
	curlCmd.Flags().StringVarP(&curlOutput, "out", "o", "", "output file path")
	curlCmd.Flags().BoolVarP(&curlVerbose, "verbose", "v", false, "stream visor's router/transport logs to stderr while the request is in flight")
	curlCmd.Flags().StringVar(&curlVLevel, "verbose-level", "debug", "minimum log level when --verbose is set: trace|debug|info|warn|error")
	RootCmd.AddCommand(curlCmd)
}

var curlCmd = &cobra.Command{
	Use:   "curl <skynet-url>",
	Short: "HTTP request over skynet",
	Long: `Make HTTP requests over skynet routes.

The visor establishes a route to the remote visor and sends the HTTP
request through the skynet forwarding server.

URL format:
  skynet://<public-key>:<port>/path
  skynet://<public-key>/path  (port defaults to 80)

Examples:
  skywire cli skynet curl skynet://02abc.../health
  skywire cli skynet curl skynet://02abc...:8000/api/ping
  skywire cli skynet curl -d '{"key":"val"}' skynet://02abc.../endpoint`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target, err := parseSkynetURL(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		method := "GET"
		var body []byte
		if curlData != "" {
			method = "POST"
			body = []byte(curlData)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), logging.MustGetLogger("skynet-curl"))
		defer cancel()

		// --verbose: subscribe to the visor's router/transport layer
		// logs BEFORE issuing the RPC. The skynet stack reuses the
		// same router as proxy/vpn, so the same module set surfaces
		// route setup, mux setup, transport probe activity. Different
		// from dmsg curl which scopes to dmsgC modules — skynet goes
		// over routes, not direct dmsg.
		var verboseStream *clirpc.VerboseStream
		if curlVerbose {
			vs, vErr := clirpc.OpenVerbose(ctx, clirpc.Addr, clirpc.VerboseFilter{
				Modules: []string{"router", "route_setup", "setup_node", "mux_setup", "transport_manager", "skynet"},
				Level:   curlVLevel,
			})
			if vErr != nil {
				internal.PrintFatalError(cmd.Flags(), vErr)
			}
			verboseStream = vs
			if err := vs.WaitSubscribed(ctx, 2*time.Second); err != nil && ctx.Err() != nil {
				return
			}
		}
		defer func() {
			if verboseStream != nil {
				verboseStream.Close()
			}
		}()

		fmt.Fprintf(os.Stderr, "Dialing %s:%d%s...\n", target.pk.String(), target.port, target.path)

		// Run RPC in a goroutine so SIGINT can interrupt the wait
		// (net/rpc.Call ignores context); same pattern as dmsg curl.
		type result struct {
			resp *visor.SkynetHTTPResponse
			err  error
		}
		done := make(chan result, 1)
		go func() {
			r, e := rpcClient.SkynetHTTP(visor.SkynetHTTPRequest{
				PK:     target.pk,
				Port:   target.port,
				Path:   target.path,
				Method: method,
				Body:   body,
			})
			done <- result{r, e}
		}()
		var resp *visor.SkynetHTTPResponse
		select {
		case <-ctx.Done():
			internal.PrintFatalError(cmd.Flags(), ctx.Err())
		case r := <-done:
			resp, err = r.resp, r.err
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("skynet request failed: %w", err))
		}

		fmt.Fprintf(os.Stderr, "%s\n", resp.Status)

		if curlOutput != "" {
			if err := os.WriteFile(curlOutput, resp.Body, 0o600); err != nil { //nolint:gosec
				internal.PrintFatalError(cmd.Flags(), err)
			}
			fmt.Fprintf(os.Stderr, "Saved to %s (%d bytes)\n", curlOutput, len(resp.Body))
		} else {
			os.Stdout.Write(resp.Body) //nolint:errcheck,gosec
			fmt.Fprintln(os.Stderr)
		}
	},
}

type skynetTarget struct {
	pk   cipher.PubKey
	port uint16
	path string
}

func parseSkynetURL(rawURL string) (skynetTarget, error) {
	u := rawURL
	if strings.HasPrefix(u, "skynet://") {
		u = strings.TrimPrefix(u, "skynet://")
	} else if strings.HasPrefix(u, "http://") {
		u = strings.TrimPrefix(u, "http://")
	}

	path := "/"
	if idx := strings.Index(u, "/"); idx >= 0 {
		path = u[idx:]
		u = u[:idx]
	}

	host := u
	port := uint16(80)
	if h, p, err := net.SplitHostPort(u); err == nil {
		host = h
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return skynetTarget{}, fmt.Errorf("invalid port: %w", err)
		}
		port = uint16(n)
	}

	host = strings.TrimSuffix(host, ".skynet")

	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return skynetTarget{}, fmt.Errorf("invalid public key %q: %w", host, err)
	}

	return skynetTarget{pk: pk, port: port, path: path}, nil
}
