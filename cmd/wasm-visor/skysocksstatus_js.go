//go:build js && wasm

// Package main cmd/wasm-visor/skysocksstatus_js.go c3-vis-wasm
// skysocksstatus_js.go — a self-contained LOCAL status page for the skysocks-lite
// proxy pool, served in-process for the `status.skysocks` host (see resolveFetchHost
// in resolve_js.go). It is served straight from the visor — NOT through the skysocks
// proxy — which is the entire point: it's the page you open to see WHY the proxy is
// (or isn't) working, so it must never be gated behind the very proxy / "Connecting
// over skywire…" interstitial it reports on. `status.skysocks` is classified as a
// mesh host by the nested browser (browse.js go()), so it takes the in-process dmsg
// path and renders instantly regardless of proxy state.
package main

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// skysocksStatusHTML renders the current skysocks-lite pool: the active exit, the
// standby(s), which exits have ever connected, per-exit recent-fail counts, and the
// fail-cooldown set (unreliable exits shed so the pool pulls fresh ones). Full public
// keys are shown, never truncated.
func skysocksStatusHTML() []byte {
	now := time.Now()

	proxyPoolMu.Lock()
	pool := append([]cipher.PubKey(nil), proxyPool...)
	proxyPoolMu.Unlock()

	proxyRegMu.Lock()
	var activeExit, detail string
	var auto bool
	var status int
	if inst := proxyReg[defaultProxyID]; inst != nil {
		activeExit, detail, auto, status = inst.Exit, inst.Detail, inst.Auto, inst.Status
	}
	proxyRegMu.Unlock()

	proxyConnMu.Lock()
	ever := make(map[cipher.PubKey]bool, len(proxyEverConnected))
	for k, v := range proxyEverConnected {
		ever[k] = v
	}
	sticky := make(map[cipher.PubKey]bool, len(proxyStickyActive))
	for k, v := range proxyStickyActive {
		sticky[k] = v
	}
	cooldown := make(map[cipher.PubKey]time.Time, len(proxyExitCooldownUntil))
	for k, v := range proxyExitCooldownUntil {
		cooldown[k] = v
	}
	fails := make(map[cipher.PubKey]int, len(proxyExitRecentFails))
	for k, v := range proxyExitRecentFails {
		fails[k] = v
	}
	proxyConnMu.Unlock()

	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>skysocks status</title>`)
	// Auto-refresh: this is a live view, and it must keep updating even while the
	// proxy it reports on is still trying to come up.
	b.WriteString(`<meta http-equiv="refresh" content="3">`)
	b.WriteString(`<style>body{font:14px/1.55 system-ui,sans-serif;background:#15151f;color:#cdd6e0;margin:0;padding:22px}` +
		`h1{font-size:19px;margin:0 0 4px}h2{font-size:15px;margin:18px 0 6px;color:#9ab}` +
		`code{color:#8fd;word-break:break-all}table{border-collapse:collapse;margin:6px 0;width:100%;max-width:920px}` +
		`td,th{padding:4px 10px;border-bottom:1px solid #2a2a38;text-align:left;font-size:13px}` +
		`.ok{color:#7e7}.bad{color:#e77}.warn{color:#ec7}.muted{opacity:.7}</style></head><body>`)
	b.WriteString(`<h1>skysocks-lite proxy status</h1>`)
	b.WriteString(fmt.Sprintf(`<p class="muted">visor <code>%s</code> · served locally, NOT through the proxy</p>`, esc(selfPK.Hex())))

	b.WriteString(`<h2>Active exit</h2>`)
	if activeExit != "" {
		cls, label := "warn", "selecting a route…"
		if status == 1 {
			cls, label = "ok", "running"
		}
		mode := "pinned"
		if auto {
			mode = "auto-selected"
		}
		b.WriteString(fmt.Sprintf(`<p class="%s"><code>%s</code> — %s (%s)</p>`, cls, esc(activeExit), label, mode))
		if detail != "" {
			b.WriteString(fmt.Sprintf(`<p class="muted">%s</p>`, esc(detail)))
		}
	} else {
		b.WriteString(`<p class="bad">no active exit selected yet — the pool is still probing candidates</p>`)
	}

	b.WriteString(`<h2>Pool</h2><table><tr><th>exit</th><th>role</th><th>ever&nbsp;connected</th><th>recent&nbsp;fails</th><th>state</th></tr>`)
	if len(pool) == 0 {
		b.WriteString(`<tr><td colspan="5" class="muted">pool empty</td></tr>`)
	}
	for i, pk := range pool {
		role := "standby"
		if i == 0 {
			role = "active"
		}
		stCls, st := "muted", "—"
		if sticky[pk] {
			stCls, st = "warn", "sticky-reconnecting"
		}
		everCls := "muted"
		everTxt := "no"
		if ever[pk] {
			everCls, everTxt = "ok", "yes"
		}
		b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td>%s</td><td class="%s">%s</td><td>%d</td><td class="%s">%s</td></tr>`,
			esc(pk.Hex()), role, everCls, everTxt, fails[pk], stCls, st))
	}
	b.WriteString(`</table>`)

	type cd struct {
		pk  cipher.PubKey
		rem time.Duration
	}
	var cds []cd
	for pk, until := range cooldown {
		if rem := until.Sub(now); rem > 0 {
			cds = append(cds, cd{pk, rem})
		}
	}
	sort.Slice(cds, func(i, j int) bool { return cds[i].rem < cds[j].rem })
	b.WriteString(fmt.Sprintf(`<h2>Fail-cooldown <span class="muted">(%d unreliable exit(s) shed so the pool pulls fresh ones)</span></h2>`, len(cds)))
	if len(cds) == 0 {
		b.WriteString(`<p class="muted">none</p>`)
	} else {
		b.WriteString(`<table><tr><th>exit</th><th>retry in</th></tr>`)
		for _, c := range cds {
			b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td>%s</td></tr>`, esc(c.pk.Hex()), c.rem.Round(time.Second)))
		}
		b.WriteString(`</table>`)
	}

	b.WriteString(`</body></html>`)
	return []byte(b.String())
}
