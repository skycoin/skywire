// Package ping cmd/skywire-cli/commands/visor/ping/tree_helpers.go
//
// Shared helpers + wire-shape types for `ping tree` and any other
// future ping subcommand that walks the transport-discovery graph.
//
// History: these used to live in graph.go (transportEntry,
// uptimeEntry, treeNeighbor) and tree.go (isTestEnv, getDeployment,
// cacheDirPath, cacheFile). Both files were removed when `ping tree2`
// — the bubbletea-based scrollable variant — replaced the older
// pterm-based `ping tree` / `ping graph` commands. The helpers
// survived the cull because they're general-purpose and still
// useful for the surviving (renamed-from-tree2) tree command and
// for the bandwidth subcommand.
//
// NOTE: cache-directory helpers (cacheDirPath/cacheFile) lived here
// briefly during the old-tree → new-tree consolidation; they let
// the old `ping tree` flag cache locations as *directories*
// (--cdt /tmp/tpd) with per-endpoint files synthesized inside
// them, vs the surviving `ping tree` flagging cache locations as
// individual *files* (--cft /tmp/tpd.json). Removed when no surviving
// caller used them; reachable via git history if a future caller
// needs multiple distinct caches under one host.
package ping

import (
	"os"

	"github.com/skycoin/skywire/deployment"
)

// transportEntry is the JSON wire shape of one entry returned by
// TPD's /all-transports endpoint (and the CXO tpd-all-transports
// feed). Mirrors transport.Entry minus the latency / bandwidth
// fields the CLI doesn't currently consume here.
type transportEntry struct {
	ID    string    `json:"t_id"`
	Type  string    `json:"type"`
	Edges [2]string `json:"edges"`
}

// uptimeEntry is the JSON wire shape of one entry returned by the
// uptime tracker's /uptimes?v=v2 endpoint. The CLI uses Online for
// the --online filter and Version for the --version filter.
type uptimeEntry struct {
	PK      string `json:"pk"`
	Online  bool   `json:"on"`
	Version string `json:"version,omitempty"`
}

// treeNeighbor is the adjacency-map entry — for a given visor PK,
// each treeNeighbor describes one of its transport-connected peers
// plus the transport's ID and type. Used in BFS expansion to find
// level-N+1 visors from level-N visors.
type treeNeighbor struct {
	pk     string
	tpID   string
	tpType string
}

// isTestEnv reports whether the SKYWIRETEST environment variable
// is set to "1", meaning the operator wants test-deployment service
// URLs by default.
func isTestEnv() bool {
	return os.Getenv("SKYWIRETEST") == "1"
}

// getDeployment returns the deployment service URLs the CLI should
// use for TPD / UT / DMSG-discovery fetches. SKYWIRETEST=1 switches
// to the test deployment; otherwise prod. The --testenv flag
// (handled at command Run time) overlays the same swap at the
// per-invocation level for operators who don't want to set the env
// var.
func getDeployment() deployment.Services {
	if isTestEnv() {
		return deployment.Test
	}
	return deployment.Prod
}
