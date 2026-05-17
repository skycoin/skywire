// Package cliskychat cmd/skywire-cli/commands/skychat/verbose.go
//
// statusCounters: minimal snapshot of the chat-app's /status counter
// surface used by `cli skychat send --verbose` to compute pre/post
// deltas around a single send. Surfacing the deltas at the CLI lets
// operators see WHICH layer absorbed a failure (HTTP retry path?
// transport-fallback path? chat-app outbound-fail path?) without
// having to grep visor logs after the fact.
//
// Why not just re-use a struct from cmd/apps/skychat/commands: that
// package is the chat-app implementation; the CLI is a thin client
// over /status JSON. Importing the chat-app's full struct here would
// pull in its dependency graph (gin, persistent-stores, …). The
// trimmed shape here is just the counters the verbose path needs.
package cliskychat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// statusCounters mirrors a slice of the chat-app's /status JSON.
// Fields with json:",omitempty" so a chat-app build that doesn't
// yet emit a particular counter doesn't error on decode — we just
// see zero and a zero delta.
type statusCounters struct {
	InboundMsgCount       uint64 `json:"inbound_msg_count"`
	InboundDropCount      uint64 `json:"inbound_drop_count"`
	OutboundMsgCount      uint64 `json:"outbound_msg_count"`
	OutboundFailCount     uint64 `json:"outbound_fail_count"`
	OutboundRetryCount    uint64 `json:"outbound_retry_count"`
	OutboundFallbackCount uint64 `json:"outbound_fallback_count"`
	SSEDropCount          uint64 `json:"sse_drop_count"`
	SSESubscribers        int    `json:"sse_subscribers"`
	ActivePeerConns       int    `json:"active_peer_conns"`
}

// summary renders one-line counter state for the verbose log. Order
// matches the operator's typical read sequence: how many sends went
// out, how many failed, how many retried, how many fell back to the
// alternate transport.
func (c *statusCounters) summary() string {
	return fmt.Sprintf("outbound={msg=%d fail=%d retry=%d fallback=%d} inbound={msg=%d drop=%d} sse={subs=%d drop=%d} peers=%d",
		c.OutboundMsgCount, c.OutboundFailCount, c.OutboundRetryCount, c.OutboundFallbackCount,
		c.InboundMsgCount, c.InboundDropCount,
		c.SSESubscribers, c.SSEDropCount,
		c.ActivePeerConns,
	)
}

// delta renders the change between this (current) snapshot and prev.
// Surfaces only non-zero deltas so a clean send shows just
// "outbound_msg=+1" and a problematic one shows the failure-mode
// counters too.
func (c *statusCounters) delta(prev *statusCounters) string {
	if prev == nil {
		return "<no baseline>"
	}
	parts := []string{}
	add := func(name string, cur, was uint64) {
		if cur != was {
			parts = append(parts, fmt.Sprintf("%s=+%d", name, cur-was))
		}
	}
	addI := func(name string, cur, was int) {
		if cur != was {
			parts = append(parts, fmt.Sprintf("%s=%+d", name, cur-was))
		}
	}
	add("outbound_msg", c.OutboundMsgCount, prev.OutboundMsgCount)
	add("outbound_fail", c.OutboundFailCount, prev.OutboundFailCount)
	add("outbound_retry", c.OutboundRetryCount, prev.OutboundRetryCount)
	add("outbound_fallback", c.OutboundFallbackCount, prev.OutboundFallbackCount)
	add("inbound_msg", c.InboundMsgCount, prev.InboundMsgCount)
	add("inbound_drop", c.InboundDropCount, prev.InboundDropCount)
	add("sse_drop", c.SSEDropCount, prev.SSEDropCount)
	addI("sse_subs", c.SSESubscribers, prev.SSESubscribers)
	addI("active_peer_conns", c.ActivePeerConns, prev.ActivePeerConns)
	if len(parts) == 0 {
		return "<no change>"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

// fetchStatusCounters GETs /status and decodes the counter subset.
// Short timeout (2s) because /status is a local-loopback fast path
// and we don't want the verbose-counter fetch to dominate the send
// command's own timing.
func fetchStatusCounters(addr string) (*statusCounters, error) {
	url := fmt.Sprintf("http://%s/status", addr)
	hc := &http.Client{Timeout: 2 * time.Second}
	resp, err := hc.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("status fetch: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		return nil, fmt.Errorf("status returned %d: %s", resp.StatusCode, string(body))
	}
	var c statusCounters
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return &c, nil
}
