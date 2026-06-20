// Package clilog cmd/skywire-cli/commands/log/root.go
package clilog

import (
	"strings"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
)

// dmsgDiscArgs splits an operator-supplied --dmsg-disc value into the
// (httpURL, dmsgURL) pair BootstrapDmsg expects. Defaults to the
// embedded dmsg URL (deployment.Prod.DmsgDiscoveryDmsg) so the
// per-PK Entry() fallback rides dmsg-HTTP instead of plain HTTP —
// finishing the dmsg-only conversion #3163/#3174 started.
//
// Selection:
//   - empty or the embedded Prod HTTP default → dmsg URL
//   - "dmsg://…" → dmsg URL passthrough
//   - any other "http(s)://…" → operator opted into a custom HTTP
//     endpoint, route it the old way
func dmsgDiscArgs(val string) (httpURL, dmsgURL string) {
	if val == "" || val == deployment.Prod.DmsgDiscovery {
		return "", deployment.Prod.DmsgDiscoveryDmsg
	}
	if strings.HasPrefix(val, "dmsg://") {
		return "", val
	}
	return val, ""
}

var (
	dmsgDiscURL    = deployment.Prod.DmsgDiscovery
	utURL          = deployment.Prod.UptimeTracker
	pubKey         string
	duration       int
	minv           string
	allVisors      bool
	batchSize      int
	maxFileSize    int64
	utAddr         string
	sk             cipher.SecKey
	dmsgDisc       string
	logOnly        bool
	surveyOnly     bool
	deleteOnErrors bool
	incVer         string
	fetchFile      string
	fetchFrom      string
	writeDir       string
	lcDir          string
	hideErr        bool
	showUT         bool
	pubKeys        []cipher.PubKey
	runCleanup     bool
	backupDir      string
	maxAgeDays     int
	pruneBelowVer  string
)

// RootCmd is logCmd
var RootCmd = logCmd

// VisorUptimeResponse represents visor uptime data.
type VisorUptimeResponse struct {
	PubKey     string  `json:"pk"`
	Uptime     float64 `json:"up"`
	Downtime   float64 `json:"down"`
	Percentage float64 `json:"pct"`
	Online     bool    `json:"on"`
	Version    string  `json:"version"`
}
