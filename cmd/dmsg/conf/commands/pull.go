// Package commands cmd/dmsg/conf/commands/pull.go c1-net-dmsg
package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	pullConfigPath string
	pullTimeout    time.Duration
)

func init() {
	pullCmd.Flags().StringVarP(&pullConfigPath, "config", "c", "deployment/services-config.json",
		"path to the services-config.json whose dmsg_servers will be rewritten")
	pullCmd.Flags().DurationVar(&pullTimeout, "timeout", 45*time.Second, "overall timeout")
	RootCmd.AddCommand(pullCmd)
}

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Update services-config.json dmsg_servers from the discovery, over dmsg",
	Long: `Fetch the live dmsg-discovery all_servers list OVER DMSG and rewrite the
dmsg_servers arrays in the deployment services-config.json.

A dmsg client is bootstrapped directly from the servers already embedded in the
binary (no plain-HTTP discovery is ever contacted), so as long as ONE embedded
server is reachable the client connects, reaches the dmsg-discovery through it,
and pulls the authoritative server list. This keeps the embedded config
self-updating and works once the plain-HTTP deployment endpoints are gone.

Run 'go generate ./deployment/' afterwards to sync the js/wasm static variant.`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		defer cancel()

		entries, err := fetchAllServersOverDmsg(ctx)
		if err != nil {
			return fmt.Errorf("fetch all_servers over dmsg: %w", err)
		}
		if len(entries) == 0 {
			return fmt.Errorf("discovery returned no servers; refusing to wipe dmsg_servers")
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Static.Hex() < entries[j].Static.Hex()
		})

		raw, err := os.ReadFile(pullConfigPath) //nolint:gosec // known repo-relative config
		if err != nil {
			return fmt.Errorf("read %s: %w", pullConfigPath, err)
		}
		updated, n := spliceDmsgServers(raw, entries)
		if n == 0 {
			return fmt.Errorf("no dmsg_servers array found in %s", pullConfigPath)
		}
		if err := os.WriteFile(pullConfigPath, updated, 0o644); err != nil { //nolint:gosec // shared repo config
			return fmt.Errorf("write %s: %w", pullConfigPath, err)
		}
		fmt.Printf("updated %d dmsg_servers array(s) in %s with %d servers (fetched over dmsg)\n",
			n, pullConfigPath, len(entries))
		fmt.Println("now run: go generate ./deployment/   (to sync the js variant)")
		return nil
	},
}

// fetchAllServersOverDmsg bootstraps a direct dmsg client from the embedded
// servers, reaches the embedded dmsg-discovery PK over dmsg, and returns its
// full server list. No plain HTTP is contacted.
func fetchAllServersOverDmsg(ctx context.Context) ([]*disc.Entry, error) {
	log := logging.MustGetLogger("dmsg-conf-pull")

	servers := deployment.Prod.ToDiscEntries()
	if len(servers) == 0 {
		return nil, fmt.Errorf("no embedded dmsg servers to bootstrap from")
	}
	discPK, err := pkFromDmsgURL(deployment.Prod.DmsgDiscoveryDmsg)
	if err != nil {
		return nil, fmt.Errorf("parse embedded dmsg_discovery_dmsg PK: %w", err)
	}

	pk, sk := cipher.GenerateKeyPair()
	// The discovery PK MUST be in the key set so the direct client can
	// resolve it locally (it has no entry in its own discovery).
	keys := cipher.PubKeys{pk, discPK}
	dClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

	dmsgConf := dmsg.DefaultConfig()
	dmsgConf.MinSessions = 1
	dmsgC, stop, err := direct.StartDmsg(ctx, log, pk, sk, dClient, dmsgConf)
	if err != nil {
		return nil, fmt.Errorf("start direct dmsg client: %w", err)
	}
	defer stop()

	dmsgURL := fmt.Sprintf("http://%s:%d", discPK.Hex(), dmsg.DefaultDmsgHTTPPort)
	hc := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
	return disc.NewHTTP(dmsgURL, hc, log).AllServers(ctx)
}

func pkFromDmsgURL(u string) (cipher.PubKey, error) {
	s := strings.TrimPrefix(u, "dmsg://")
	if i := strings.IndexAny(s, ":/"); i >= 0 {
		s = s[:i]
	}
	var pk cipher.PubKey
	return pk, pk.Set(s)
}

// dmsgServersRe matches a `"dmsg_servers": [ ... ]` array up to its closing
// bracket at the 4-space deployment-section indent. The entries contain no
// nested arrays, so the non-greedy match stops at the array's own ].
var dmsgServersRe = regexp.MustCompile(`(?s)"dmsg_servers": \[.*?\n    \]`)

func spliceDmsgServers(content []byte, entries []*disc.Entry) ([]byte, int) {
	replacement := []byte(formatDmsgServers(entries))
	count := 0
	out := dmsgServersRe.ReplaceAllFunc(content, func(_ []byte) []byte {
		count++
		return replacement
	})
	return out, count
}

func formatDmsgServers(entries []*disc.Entry) string {
	var b strings.Builder
	b.WriteString(`"dmsg_servers": [`)
	for i, e := range entries {
		b.WriteString("\n      {\n")
		fmt.Fprintf(&b, "        \"static\": %q,\n", e.Static.Hex())
		b.WriteString("        \"server\": {\n")
		fmt.Fprintf(&b, "          \"address\": %q\n", e.Server.Address)
		b.WriteString("        }\n      }")
		if i < len(entries)-1 {
			b.WriteString(",")
		}
	}
	b.WriteString("\n    ]")
	return b.String()
}
