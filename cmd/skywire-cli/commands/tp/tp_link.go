// Package clitp cmd/skywire-cli/commands/tp/tp_link.go
package clitp

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/transport/network"
)

var showLink bool

// linkHeader is appended to the table header when --link is set.
func linkHeader() string {
	if !showLink {
		return ""
	}
	return "\tsrtt\tloss\tlost/sent\tcwnd"
}

// linkCells renders the carrier link stats from a transport's endpoint
// details, "-" for what the carrier does not report.
func linkCells(ep *network.ConnDetails) string {
	if !showLink {
		return ""
	}
	srtt, loss, lostSent, cwnd := "-", "-", "-", "-"
	if ep != nil && ep.Link != nil {
		l := ep.Link
		if l.SRTTMs > 0 {
			srtt = fmt.Sprintf("%.0fms", l.SRTTMs)
		}
		if l.Loss != nil {
			loss = fmt.Sprintf("%.2f%%", l.Loss.Pct)
			lostSent = fmt.Sprintf("%d/%d", l.Loss.PacketsLost, l.Loss.PacketsSent)
		}
		if l.CwndBytes > 0 {
			cwnd = formatBytes(l.CwndBytes)
		}
	}
	return "\t" + srtt + "\t" + loss + "\t" + lostSent + "\t" + cwnd
}
