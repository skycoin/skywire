// Package cliskychat cmd/skywire-cli/commands/skychat/sendfile.go
//
// `skywire-cli skychat send-file` sends a local file to a peer (1:1) or to the
// active CXO group, via the running skychat app's HTTP API (POST /send-file).
// The file itself streams peer-to-peer over an encrypted skywire transport
// (skyenv.SkychatFilePort, skynet or dmsg) — the HTTP call only hands the app a
// local path to read. Files are conversation messages: the transfer surfaces in
// history / SSE like any other message.
package cliskychat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	sendFileTo    string
	sendFileGroup bool
)

var sendFileCmd = &cobra.Command{
	Use:   "send-file <path>",
	Short: "send a file to a peer (--to) or the active group (--group)",
	Long: `Send a local file over skychat.

The file streams peer-to-peer over an encrypted skywire transport (skynet or
dmsg); it never touches the clearnet. A peer auto-accepts once you've already
established a chat, and group members auto-accept files shared to a group they're
in — no per-file prompt.

Requires the skychat app to be running (default --addr 127.0.0.1:8001).

Examples:
  skywire-cli skychat send-file --to <peer-pk> ./photo.png
  skywire-cli skychat send-file --group ./release.tar.gz`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path, err := filepath.Abs(args[0])
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), fmt.Errorf("resolve path: %w", err))
		}
		if !sendFileGroup && sendFileTo == "" {
			cliutil.PrintFatalError(cmd.Flags(), fmt.Errorf("specify --to <pk> or --group"))
		}
		form := url.Values{}
		form.Set("path", path)
		if sendFileGroup {
			form.Set("group", "1")
		} else {
			var pk cipher.PubKey
			if err := pk.Set(strings.TrimSpace(sendFileTo)); err != nil {
				cliutil.PrintFatalError(cmd.Flags(), fmt.Errorf("--to: %w", err))
			}
			form.Set("pk", pk.Hex())
		}

		endpoint := fmt.Sprintf("http://%s/send-file", httpAddr)
		hc := &http.Client{Timeout: 35 * time.Minute}
		resp, err := hc.PostForm(endpoint, form)
		if err != nil {
			cliutil.PrintFatalError(cmd.Flags(), fmt.Errorf("POST %s: %w", endpoint, err))
		}
		defer func() { _ = resp.Body.Close() }() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)         //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			cliutil.PrintFatalError(cmd.Flags(), fmt.Errorf("send-file failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body))))
		}

		var out map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(body), &out); err != nil {
			fmt.Println(strings.TrimSpace(string(body)))
			return
		}
		if n, ok := out["dispatched"]; ok {
			fmt.Printf("file dispatched to %v group member(s)\n", n)
			return
		}
		fmt.Printf("file sent (id %v)\n", out["id"])
	},
}

func init() {
	sendFileCmd.Flags().StringVarP(&sendFileTo, "to", "t", "", "recipient public key (1:1)")
	sendFileCmd.Flags().BoolVarP(&sendFileGroup, "group", "g", false, "send to every member of the active CXO group")
	RootCmd.AddCommand(sendFileCmd)
}
