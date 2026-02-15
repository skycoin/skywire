// Package clidmsg cmd/skywire-cli/commands/dmsg/curl.go
package clidmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	curlData    string
	curlOutput  string
	curlAgent   string
	curlTries   int
	curlWait    int
	curlReplace bool
)

func init() {
	curlCmd.Flags().SortFlags = false
	curlCmd.Flags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	curlCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal", "[ debug | warn | error | fatal | panic | trace | info ]")
	curlCmd.Flags().VarP(&sk, "sk", "s", "use a custom secret key (starts new dmsg client instead of using visor's)")
	curlCmd.Flags().StringVarP(&curlData, "data", "d", "", "HTTP POST data")
	curlCmd.Flags().StringVarP(&curlOutput, "out", "o", "", "output filepath")
	curlCmd.Flags().BoolVarP(&curlReplace, "replace", "r", false, "replace existing output file")
	curlCmd.Flags().IntVarP(&curlTries, "try", "t", 1, "download attempts (0 unlimits)")
	curlCmd.Flags().IntVarP(&curlWait, "wait", "w", 0, "time to wait between attempts (seconds)")
	curlCmd.Flags().StringVarP(&curlAgent, "agent", "a", "skywire-cli/"+buildinfo.Version(), "HTTP user agent")
	if os.Getenv("DMSG_SK") != "" {
		sk.Set(os.Getenv("DMSG_SK")) //nolint
	}
}

var curlCmd = &cobra.Command{
	Use:   "curl <dmsg-url>",
	Short: "Fetch data over dmsg",
	Long: `DMSG curl - fetch data over dmsg network.

By default uses the local visor's dmsg client via RPC.
Use --sk flag to start a standalone dmsg client instead.

Example URLs:
  dmsg://<public-key>:<port>/path
  dmsg://<public-key>/path  (port defaults to 80)`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		log := logging.MustGetLogger("dmsg-curl")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		// Parse URL
		parsedURL, err := url.Parse(args[0])
		if err != nil {
			return fmt.Errorf("failed to parse URL: %w", err)
		}

		// Validate URL scheme
		if parsedURL.Scheme != "dmsg" {
			return fmt.Errorf("invalid URL scheme: expected 'dmsg', got '%s'", parsedURL.Scheme)
		}

		// Extract destination from URL host
		destSlc := strings.Split(parsedURL.Host, ":")
		if len(destSlc) == 1 {
			destSlc = append(destSlc, "80")
		}
		var destPK cipher.PubKey
		if err := destPK.Set(destSlc[0]); err != nil {
			return fmt.Errorf("invalid public key in URL: %w", err)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// Check if we're using standalone mode (--sk flag provided) or visor mode
		pk, skErr := sk.PubKey()
		if skErr != nil {
			// No valid SK provided, use visor's dmsg client via RPC
			return curlViaVisor(cmd, ctx, log, args[0])
		}

		// Standalone mode - start new dmsg client with provided SK
		log.Info("Starting standalone dmsg client")
		return curlStandalone(ctx, log, pk, sk, parsedURL)
	},
}

// curlViaVisor performs the curl request using the visor's dmsg client via RPC
func curlViaVisor(cmd *cobra.Command, _ context.Context, log *logging.Logger, dmsgURL string) error {
	rpcClient, err := rpcClient(cmd)
	if err != nil {
		return fmt.Errorf("RPC connection failed; is skywire running?: %w", err)
	}
	defer rpcClient.Close() //nolint:errcheck

	// Prepare output
	output := os.Stdout
	if curlOutput != "" {
		var err error
		output, err = prepareOutputFile(curlOutput, curlReplace)
		if err != nil {
			return fmt.Errorf("failed to prepare output file: %w", err)
		}
		defer output.Close() //nolint:errcheck
	}

	// Prepare request
	req := visor.DmsgHTTPRequest{
		URL:    dmsgURL,
		Method: http.MethodGet,
		Header: map[string]string{
			"User-Agent": curlAgent,
		},
	}
	if curlData != "" {
		req.Method = http.MethodPost
		req.Body = []byte(curlData)
		req.Header["Content-Type"] = "text/plain"
	}

	// Make request via visor
	tries := curlTries
	if tries == 0 {
		tries = 1<<31 - 1 // Effectively unlimited
	}

	var lastErr error
	for i := 0; i < tries; i++ {
		if i > 0 {
			log.Debugf("Attempt %d/%d...", i+1, curlTries)
			time.Sleep(time.Duration(curlWait) * time.Second)
		}

		resp, err := rpcClient.DmsgHTTP(req)
		if err != nil {
			lastErr = err
			log.WithError(err).Debug("Request failed")
			continue
		}

		// Write response
		if resp.StatusCode >= 400 {
			log.Warnf("HTTP %d: %s", resp.StatusCode, resp.Status)
		}

		_, err = output.Write(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}

		if curlOutput != "" {
			fmt.Printf("Downloaded %d bytes to %s\n", len(resp.Body), curlOutput)
		}
		return nil
	}

	return fmt.Errorf("all attempts failed: %w", lastErr)
}

// curlStandalone performs the curl request using a standalone dmsg client
func curlStandalone(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, parsedURL *url.URL) error {
	// Start dmsg client
	dmsgC, closeDmsg, err := startDmsgClient(ctx, log, pk, sk)
	if err != nil {
		return fmt.Errorf("failed to start dmsg client: %w", err)
	}
	defer closeDmsg()

	// Create HTTP client with dmsg transport
	httpClient := &http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC),
	}

	// Prepare output
	output := os.Stdout
	if curlOutput != "" {
		var err error
		output, err = prepareOutputFile(curlOutput, curlReplace)
		if err != nil {
			return fmt.Errorf("failed to prepare output file: %w", err)
		}
		defer output.Close() //nolint:errcheck
	}

	// Make request
	tries := curlTries
	if tries == 0 {
		tries = 1<<31 - 1 // Effectively unlimited
	}

	var lastErr error
	for i := 0; i < tries; i++ {
		if i > 0 {
			log.Debugf("Attempt %d/%d...", i+1, curlTries)
			time.Sleep(time.Duration(curlWait) * time.Second)
		}

		var req *http.Request
		var err error
		if curlData != "" {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), strings.NewReader(curlData))
			if err == nil {
				req.Header.Set("Content-Type", "text/plain")
			}
		} else {
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
		}
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("User-Agent", curlAgent)

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.WithError(err).Debug("Request failed")
			if isFatalHTTPErr(err) {
				return fmt.Errorf("fatal error: %w", err)
			}
			continue
		}
		defer resp.Body.Close() //nolint:errcheck

		// Write response
		if resp.StatusCode >= 400 {
			log.Warnf("HTTP %d: %s", resp.StatusCode, resp.Status)
		}

		n, err := io.Copy(output, resp.Body)
		if err != nil {
			lastErr = err
			log.WithError(err).Debug("Failed to read response")
			continue
		}

		if curlOutput != "" {
			fmt.Printf("Downloaded %d bytes to %s\n", n, curlOutput)
		}
		return nil
	}

	return fmt.Errorf("all attempts failed: %w", lastErr)
}

// startDmsgClient starts a standalone dmsg client
func startDmsgClient(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey) (*dmsg.Client, func(), error) {
	// Use production dmsg servers
	if len(dmsg.Prod.DmsgServers) == 0 {
		return nil, nil, fmt.Errorf("no DMSG servers configured")
	}

	// Create discovery client using HTTP
	discURL := "http://dmsgd.skywire.skycoin.com"
	httpClient := &http.Client{Timeout: 30 * time.Second}
	discClient := disc.NewHTTP(discURL, httpClient, log)

	// Create dmsg client
	dmsgConfig := dmsg.DefaultConfig()
	dmsgConfig.MinSessions = 1

	dmsgC := dmsg.NewClient(pk, sk, discClient, dmsgConfig)
	go dmsgC.Serve(ctx)

	// Wait for ready
	select {
	case <-ctx.Done():
		_ = dmsgC.Close() //nolint:errcheck
		return nil, nil, ctx.Err()
	case <-dmsgC.Ready():
		log.Debug("DMSG client ready")
	case <-time.After(30 * time.Second):
		_ = dmsgC.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("timeout waiting for dmsg client")
	}

	stop := func() {
		_ = dmsgC.Close() //nolint:errcheck
	}

	return dmsgC, stop, nil
}

func rpcClient(_ *cobra.Command) (visor.API, error) {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		return nil, err
	}
	log := logging.MustGetLogger("rpc")
	return visor.NewRPCClient(log, conn, visor.RPCPrefix, 0), nil
}

func prepareOutputFile(path string, replace bool) (*os.File, error) {
	_, err := os.Stat(path)
	if err == nil {
		// File exists
		if !replace {
			return nil, fmt.Errorf("file already exists: %s (use -r to replace)", path)
		}
		return os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644) //nolint:gosec
	}
	if os.IsNotExist(err) {
		return os.Create(path) //nolint:gosec
	}
	return nil, err
}

func isFatalHTTPErr(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}
