// Package ui2 pkg/visor/ui2/info.go c3-vis-core
package ui2

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Info is the info tab: the visor's summary and the settings shown under it.
type Info struct {
	PK        string
	Sum       visorapi.Summary
	Err       string
	Ports     []KV
	Router    *visorapi.RouterSettings
	RouterErr string
}

// Alert is one line of the overview's attention list.
type Alert struct {
	Warn bool
	Text string
}

// healthGrace is how long the visor's own checks may stay "connecting"
// before the overview calls them a problem, as the Angular overview does.
const healthGrace = 600

// HealthClass names a health value: ok, bad, or still waiting.
func HealthClass(s string) string {
	switch s {
	case "healthy":
		return "ok"
	case "unhealthy":
		return "bad"
	}
	return "wait"
}

func (i Info) ov() *visorapi.Overview {
	if i.Sum.Overview == nil {
		return &visorapi.Overview{}
	}
	return i.Sum.Overview
}

func (i Info) health() *visorapi.HealthInfo {
	if i.Sum.Health == nil {
		return &visorapi.HealthInfo{}
	}
	return i.Sum.Health
}

// MeshName is the visor's .skynet and .dmsg host name.
func (i Info) MeshName() string {
	var pk cipher.PubKey
	if pk.Set(i.PK) != nil {
		return ""
	}
	return pk.DNSLabel()
}

// Version is the running build's version, or "".
func (i Info) Version() string {
	if b := i.ov().BuildInfo; b != nil {
		return b.Version
	}
	return ""
}

func (i Info) build() (os, arch string) {
	if b := i.ov().BuildInfo; b != nil {
		return b.OS, b.Arch
	}
	return "", ""
}

// TransportTypes counts the transports by type, most first.
func (i Info) TransportTypes() []TypeCount {
	n := map[string]int{}
	for _, t := range i.ov().Transports {
		if t != nil {
			n[string(t.Type)]++
		}
	}
	out := make([]TypeCount, 0, len(n))
	for k, v := range n {
		out = append(out, TypeCount{k, v})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].Type < out[b].Type
	})
	return out
}

// Directions counts incoming and outgoing transports.
func (i Info) Directions() (in, out int) {
	for _, t := range i.ov().Transports {
		if t == nil {
			continue
		}
		if t.Initiator {
			out++
		} else {
			in++
		}
	}
	return in, out
}

// AppCounts is how many apps run, start with the visor, and failed.
func (i Info) AppCounts() (running, autostart, failed int) {
	for _, a := range i.ov().Apps {
		if a == nil {
			continue
		}
		switch a.Status {
		case appserver.AppStatusRunning:
			running++
		case appserver.AppStatusErrored:
			failed++
		}
		if a.AutoStart {
			autostart++
		}
	}
	return running, autostart, failed
}

// Alerts follows overviewAlerts of the Angular overview.
func (i Info) Alerts(ctx context.Context) []Alert {
	var out []Alert
	if c := i.Sum.Connectivity; c != nil {
		switch c.State {
		case visorapi.ConnOffline:
			out = append(out, Alert{true, T(ctx, "node.overview.alert-offline")})
		case visorapi.ConnDmsgOnly:
			out = append(out, Alert{false, T(ctx, "node.overview.alert-dmsg-only")})
		}
	}
	settled := i.Sum.Uptime >= healthGrace
	h := i.health()
	for _, c := range []struct{ state, key string }{
		{h.UptimeTrackerHealth, "node.overview.alert-uptime-tracker"},
		{h.AutoconnectHealth, "node.overview.alert-autoconnect"},
		{h.TransportabilityHealth, "node.overview.alert-transportability"},
	} {
		if c.state != "" && c.state != "healthy" {
			out = append(out, Alert{settled, T(ctx, c.key)})
		}
	}
	for _, a := range i.ov().Apps {
		if a == nil {
			continue
		}
		switch {
		case a.Status == appserver.AppStatusErrored:
			out = append(out, Alert{true, T(ctx, "node.overview.alert-app-failed", "app", a.Name)})
		case a.AutoStart && a.Status != appserver.AppStatusRunning && a.Status != appserver.AppStatusStarting:
			if why := strings.TrimSpace(a.DetailedStatus); why != "" {
				out = append(out, Alert{true, T(ctx, "node.overview.alert-app-stopped-why", "app", a.Name, "why", why)})
			} else {
				out = append(out, Alert{true, T(ctx, "node.overview.alert-app-stopped", "app", a.Name)})
			}
		}
	}
	if i.Sum.Overview != nil && len(i.ov().Transports) == 0 {
		out = append(out, Alert{true, T(ctx, "node.overview.alert-no-transports")})
	}
	if i.Sum.Overview != nil && len(i.Sum.DMSGServers) == 0 {
		out = append(out, Alert{true, T(ctx, "node.overview.alert-no-dmsg")})
	}
	switch {
	case i.Sum.RewardAddress == "":
		out = append(out, Alert{false, T(ctx, "node.overview.alert-no-reward")})
	case i.Sum.RewardEligible != nil && !*i.Sum.RewardEligible:
		reason := i.Sum.RewardIneligibleReason
		if reason == "" {
			reason = "-"
		}
		out = append(out, Alert{true, T(ctx, "node.overview.alert-reward-ineligible", "reason", reason)})
	}
	if v, c := i.Version(), i.Sum.ConfigVersion; v != "" && c != "" && !SameRelease(v, c) {
		out = append(out, Alert{false, T(ctx, "node.overview.alert-config-version", "config", c)})
	}
	if i.ov().SymmetricNAT {
		out = append(out, Alert{false, T(ctx, "node.overview.alert-symmetric-nat")})
	}
	return out
}

var releaseRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)

// SameRelease reports whether a visor version and the version that wrote its
// config are one release: a build adds its commit to the version the config
// records, which is not a mismatch.
func SameRelease(version, configVersion string) bool {
	a, b := releaseRe.FindStringSubmatch(version), releaseRe.FindStringSubmatch(configVersion)
	if a != nil && b != nil {
		return a[1] == b[1]
	}
	return strings.HasPrefix(version, configVersion)
}

// TimeOnline is the visor's uptime in the largest whole unit, as the Angular
// tab words it.
func TimeOnline(ctx context.Context, secs float64) string {
	s := int64(secs)
	unit, div := "second", int64(1)
	switch {
	case s >= 604800:
		unit, div = "week", 604800
	case s >= 86400:
		unit, div = "day", 86400
	case s >= 3600:
		unit, div = "hour", 3600
	case s >= 60:
		unit, div = "minute", 60
	}
	n := s / div
	if unit == "second" || n > 1 {
		unit += "s"
	}
	return T(ctx, "node.details.node-info.time."+unit, "time", strconv.FormatInt(n, 10))
}

// Latency prints a dmsg server's latency, or "" when not measured.
func Latency(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Millisecond {
		return strconv.FormatInt(d.Microseconds(), 10) + " µs"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + " ms"
}

// LatencyClass marks slow servers the way the Angular tab does.
func LatencyClass(d time.Duration) string {
	switch {
	case d > 800*time.Millisecond:
		return "error"
	case d > 300*time.Millisecond:
		return "warn-text"
	}
	return ""
}

// Hypervisor is one hypervisor key the visor knows, and how.
type Hypervisor struct {
	PK    string
	Local bool   // the hypervisor serving this page
	State string // connected, disconnected or runtime
}

// Hypervisors merges the configured and the connected hypervisors.
func (i Info) Hypervisors(local string) []Hypervisor {
	o := i.ov()
	conn := map[cipher.PubKey]bool{}
	for _, k := range o.ConnectedHypervisor {
		conn[k] = true
	}
	var out []Hypervisor
	seen := map[cipher.PubKey]bool{}
	for _, k := range o.Hypervisors {
		seen[k] = true
		st := "disconnected"
		if conn[k] {
			st = "connected"
		}
		out = append(out, Hypervisor{k.Hex(), k.Hex() == local, st})
	}
	for _, k := range o.ConnectedHypervisor {
		if !seen[k] {
			out = append(out, Hypervisor{k.Hex(), k.Hex() == local, "runtime"})
		}
	}
	return out
}

// PortRows sorts the visor's ports for display.
func PortRows(ports map[string]visorapi.PortDetail) []KV {
	out := make([]KV, 0, len(ports))
	for name, p := range ports {
		v := p.Port
		if p.Type != "" {
			v += " (" + p.Type + ")"
		}
		out = append(out, KV{name, v})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

func yesNo(ctx context.Context, b bool) string {
	if b {
		return T(ctx, "common.yes")
	}
	return T(ctx, "common.no")
}

func orUnknown(ctx context.Context, s string) string {
	if s == "" {
		return T(ctx, "common.unknown")
	}
	return s
}
