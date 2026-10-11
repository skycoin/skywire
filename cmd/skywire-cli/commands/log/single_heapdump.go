// Package clilog cmd/skywire-cli/commands/log/single_heapdump.go c4-vis-cli
//
// `cli log heapdump <pk>` — ask a remote visor to write a full heap dump
// to its own disk. The dump itself is never sent over dmsg.
package clilog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
)

func init() {
	RootCmd.AddCommand(singleHeapdumpCmd)
}

var singleHeapdumpCmd = &cobra.Command{
	Use:   "heapdump <pk>",
	Short: "Have a remote visor write a heap dump to its own disk",
	Long: `Ask one visor over dmsghttp to write a full heap dump (POST /debug/heapdump)
into its log directory, and print the path, size and time taken.

The dump is 1 to 2 GB and stops the visor for about 10 seconds while it is
written, so it is never streamed over dmsg. Fetch it with the pty file
commands if needed, then read it with a heap dump viewer.

Gated by the remote visor's survey_whitelist.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		hc, cleanup, err := dmsgHTTPClient(ctx, 5*time.Minute)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		out, err := postSurveyText(ctx, hc, pk, "/debug/heapdump")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprint(os.Stdout, out) //nolint:errcheck
	},
}

// postSurveyText POSTs dmsg://<pk>:80<path> and returns the short text body.
func postSurveyText(ctx context.Context, hc *http.Client, pk cipher.PubKey, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("dmsg://%s:80%s", pk, path), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := hc.Do(req) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("dmsghttp dial: %w", err)
	}
	defer resp.Body.Close()                                //nolint:errcheck
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST dmsg://%s%s returned status %d: %s", pk, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}
