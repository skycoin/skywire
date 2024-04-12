// Package clilog cmd/skywire-cli/commands/log/root.go
package clilog

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

var (
	pubKey         string
	env            string
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
)

func init() {
	RootCmd.AddCommand(
		stCmd, tpCmd,
	)
}

// RootCmd is logCmd
var RootCmd = logCmd

type VisorUptimeResponse struct { //nolint
	PubKey     string  `json:"pk"`
	Uptime     float64 `json:"up"`
	Downtime   float64 `json:"down"`
	Percentage float64 `json:"pct"`
	Online     bool    `json:"on"`
	Version    string  `json:"version"`
}
