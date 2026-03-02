//go:build js && wasm

package ui

import (
	"strings"
	"syscall/js"
)

// updateNodeInfo updates the node info panel when a node is selected
func (a *App) updateNodeInfo(node *Node) {
	// Show both panels
	SetVisible("node-info-panel", true)
	el := getElement("selected-info")
	if !el.IsNull() && !el.IsUndefined() {
		el.Get("classList").Call("add", "visible")
	}

	// Node info panel (detail view)
	SetText("node-info-pk", node.ID)

	statusText, statusColor := "Unknown", ColorUnknownBg
	switch node.Status {
	case StatusOnline:
		statusText, statusColor = "Online", ColorOnlineBg
	case StatusOffline:
		statusText, statusColor = "Offline", ColorOfflineBg
	}
	SetText("node-info-status", statusText)
	dot := getElement("node-info-status-dot")
	if !dot.IsNull() && !dot.IsUndefined() {
		dot.Get("style").Set("background", statusColor)
	}

	SetText("node-info-transports", itoa(node.ConnectionCount))
	SetText("node-info-stcpr", itoa(node.STCPRCount))
	SetText("node-info-sudph", itoa(node.SUDPHCount))
	SetText("node-info-dmsg", itoa(node.DMSGCount))

	country := node.Country
	if country == "" {
		country = "-"
	}
	SetText("node-info-country", country)

	version := node.Version
	if version == "" {
		version = "-"
	}
	SetText("node-info-version", version)

	svcText := "-"
	switch node.Service {
	case ServiceVPN:
		svcText = "VPN"
	case ServiceProxy:
		svcText = "Proxy"
	}
	if node.IsLocalVisor {
		svcText = "Local Visor"
	}
	SetText("node-info-services", svcText)

	// Selected info panel (TypeScript style)
	SetText("selected-pk", node.ID)
	SetText("selected-status", statusText)
	SetText("selected-version", version)
	SetText("selected-country", country)
	SetText("selected-conn-count", itoa(node.ConnectionCount))

	// Services badges
	var svcHTML strings.Builder
	if node.Service == ServiceVPN {
		svcHTML.WriteString(`<span class="service-badge vpn">VPN</span>`)
	}
	if node.Service == ServiceProxy {
		svcHTML.WriteString(`<span class="service-badge proxy">Proxy</span>`)
	}
	SetHTML("selected-services", svcHTML.String())

	// Build connected visors list
	var connHTML strings.Builder
	edges := a.graph.GetEdgesForNode(node.ID)
	for _, edge := range edges {
		otherPK := edge.From
		if edge.From == node.ID {
			otherPK = edge.To
		}

		tpClass := "stcpr"
		tpName := "STCPR"
		switch edge.Type {
		case TransportSUDPH:
			tpClass = "sudph"
			tpName = "SUDPH"
		case TransportDMSG:
			tpClass = "dmsg"
			tpName = "DMSG"
		}

		connHTML.WriteString(`<div class="conn-item">`)
		connHTML.WriteString(`<span class="conn-type `)
		connHTML.WriteString(tpClass)
		connHTML.WriteString(`">`)
		connHTML.WriteString(tpName)
		connHTML.WriteString(`</span>`)
		connHTML.WriteString(shortPK(otherPK))
		connHTML.WriteString(`</div>`)
	}
	SetHTML("conn-list", connHTML.String())
}

// updateSidebarStats updates transport and uptime statistics in the sidebar
func (a *App) updateSidebarStats() {
	_, _, unknown := a.graph.CountByStatus()
	stcpr, sudph, dmsg := a.graph.CountByType()

	// Transport Discovery section
	SetText("total-transports", itoa(a.graph.EdgeCount()))
	SetText("total-visors", itoa(a.graph.NodeCount()))
	SetText("count-stcpr", itoa(stcpr))
	SetText("count-sudph", itoa(sudph))
	SetText("count-dmsg", itoa(dmsg))
	SetText("count-unknown", itoa(unknown)) // Graph visors not in UT

	// Uptime Tracker section - show totals from uptime data
	SetText("count-online", itoa(a.uptimeOnlineCount))
	SetText("count-offline", itoa(a.uptimeOfflineCount))

	// Update version stats
	a.updateVersionStats()
}

// updateVersionStats updates the version distribution table
func (a *App) updateVersionStats() {
	versions := make(map[string]int)
	for _, node := range a.graph.Nodes {
		if node.Version != "" {
			versions[node.Version]++
		}
	}

	// Build version stats HTML
	var html strings.Builder
	for ver, count := range versions {
		html.WriteString(`<div class="stat"><span class="stat-label">`)
		html.WriteString(ver)
		html.WriteString(`</span><span class="stat-value">`)
		html.WriteString(itoa(count))
		html.WriteString(`</span></div>`)
	}
	SetHTML("version-stats", html.String())
}

// updateVisorList updates the visor list in the sidebar
func (a *App) updateVisorList() {
	nodes := a.graph.SortedNodes()
	SetText("visor-list-count", "("+itoa(len(nodes))+")")

	var html strings.Builder
	for _, node := range nodes {
		statusColor := ColorUnknownBg
		switch node.Status {
		case StatusOnline:
			statusColor = ColorOnlineBg
		case StatusOffline:
			statusColor = ColorOfflineBg
		}

		pk := node.ID

		html.WriteString(`<div class="visor-item" onclick="focusVisor('`)
		html.WriteString(pk)
		html.WriteString(`')">`)
		html.WriteString(`<div class="visor-status-dot" style="background:`)
		html.WriteString(statusColor)
		html.WriteString(`"></div>`)

		// Country flag
		if node.Country != "" {
			flag := countryToFlag(node.Country)
			if flag != "" {
				html.WriteString(`<span class="visor-flag">`)
				html.WriteString(flag)
				html.WriteString(`</span>`)
			}
		}

		html.WriteString(`<span class="visor-pk">`)
		html.WriteString(shortPK(pk))
		html.WriteString(`</span>`)

		// Service badges
		if node.HasServices {
			html.WriteString(`<span class="visor-services">`)
			switch node.Service {
			case ServiceVPN:
				html.WriteString(`<span class="service-badge vpn">V</span>`)
			case ServiceProxy:
				html.WriteString(`<span class="service-badge proxy">P</span>`)
			}
			html.WriteString(`</span>`)
		}

		// Transport type count badges
		html.WriteString(`<span class="visor-services">`)
		if node.STCPRCount > 0 {
			html.WriteString(`<span class="tp-badge stcpr">S`)
			html.WriteString(itoa(node.STCPRCount))
			html.WriteString(`</span>`)
		}
		if node.SUDPHCount > 0 {
			html.WriteString(`<span class="tp-badge sudph">U`)
			html.WriteString(itoa(node.SUDPHCount))
			html.WriteString(`</span>`)
		}
		if node.DMSGCount > 0 {
			html.WriteString(`<span class="tp-badge dmsg">D`)
			html.WriteString(itoa(node.DMSGCount))
			html.WriteString(`</span>`)
		}
		html.WriteString(`</span>`)

		// Version
		if node.Version != "" {
			html.WriteString(`<span class="visor-version">`)
			html.WriteString(node.Version)
			html.WriteString(`</span>`)
		}

		if node.Country != "" {
			html.WriteString(`<span class="visor-country">`)
			html.WriteString(node.Country)
			html.WriteString(`</span>`)
		}

		html.WriteString(`<span class="visor-count">`)
		html.WriteString(itoa(node.ConnectionCount))
		html.WriteString(`</span></div>`)
	}

	SetHTML("visor-list", html.String())
}

// updateLocalTransportList updates the local visor transport list
func (a *App) updateLocalTransportList() {
	if a.localVisorData == nil {
		return
	}

	var html strings.Builder

	if len(a.localVisorData.Transports) == 0 {
		html.WriteString(`<div style="color:#555;font-size:0.8em;padding:8px;text-align:center;">No transports established</div>`)
	} else {
		for _, tp := range a.localVisorData.Transports {
			// Transport type badge colors (matching TypeScript)
			badgeBg := "rgba(255,209,102,0.3)"
			badgeColor := "#ffd166"
			tpUpper := strings.ToUpper(tp.Type)
			switch tp.Type {
			case "stcpr":
				badgeBg = "rgba(0,217,165,0.3)"
				badgeColor = "#00d9a5"
			case "sudph":
				badgeBg = "rgba(0,180,216,0.3)"
				badgeColor = "#00b4d8"
			case "dmsg":
				badgeBg = "rgba(255,209,102,0.3)"
				badgeColor = "#ffd166"
			}

			// TypeScript style transport item with badge
			html.WriteString(`<div style="display:flex;align-items:center;padding:6px 8px;border-bottom:1px solid rgba(255,255,255,0.05);cursor:pointer;" onclick="focusVisor('`)
			html.WriteString(tp.RemotePK)
			html.WriteString(`')">`)
			// Type badge
			html.WriteString(`<span style="background:` + badgeBg + `;color:` + badgeColor + `;font-size:0.7em;padding:2px 6px;border-radius:3px;font-weight:600;margin-right:8px;">` + tpUpper + `</span>`)
			// Remote PK
			html.WriteString(`<span style="flex:1;color:#aaa;font-family:monospace;font-size:0.8em;overflow:hidden;text-overflow:ellipsis;">` + shortPK(tp.RemotePK) + `...</span>`)
			// Traffic stats
			html.WriteString(`<span style="color:#00d9a5;font-size:0.7em;margin-left:6px;">↑` + formatBytes(tp.SentBytes) + `</span>`)
			html.WriteString(`<span style="color:#00b4d8;font-size:0.7em;margin-left:4px;">↓` + formatBytes(tp.RecvBytes) + `</span>`)
			html.WriteString(`</div>`)
		}
	}

	SetHTML("local-transport-list", html.String())
}

// updateDMSGServerList updates the DMSG server list in the sidebar
func (a *App) updateDMSGServerList() {
	if a.dmsgData == nil {
		return
	}

	var html strings.Builder
	html.WriteString(`<table style="width:100%;border-collapse:collapse;">`)
	html.WriteString(`<tr style="color:#888;font-size:0.9em;border-bottom:1px solid #0f3460;">`)
	html.WriteString(`<td style="padding:2px 0;">Server</td>`)
	html.WriteString(`<td style="text-align:right;padding:2px 4px;">Sess</td>`)
	html.WriteString(`<td style="text-align:right;padding:2px 0;">Clients</td>`)
	html.WriteString(`</tr>`)

	for _, srv := range a.dmsgData.Servers {
		sessions := srv.AvailableSessions
		clients := len(srv.Clients)
		shortPK := shortPK(srv.PK)
		nodeID := "dmsg-srv-" + srv.PK

		// Session color based on availability
		sessionColor := ColorOfflineBg
		if sessions > 50 {
			sessionColor = ColorOnlineBg
		} else if sessions > 10 {
			sessionColor = ColorUnknownBg
		}

		// Country flag emoji if available
		flag := countryToFlag(srv.Country)

		html.WriteString(`<tr style="border-bottom:1px solid #0f3460;">`)
		html.WriteString(`<td style="padding:2px 0;">`)
		html.WriteString(`<span title="` + srv.PK + `" style="cursor:pointer;color:#9f6efc;" onclick="focusVisor('` + nodeID + `')">`)
		html.WriteString(flag + " " + shortPK)
		html.WriteString(`</span></td>`)
		html.WriteString(`<td style="text-align:right;padding:2px 4px;color:` + sessionColor + `;">`)
		html.WriteString(itoa(sessions))
		html.WriteString(`</td>`)
		html.WriteString(`<td style="text-align:right;padding:2px 0;color:#6ec8ff;">`)
		html.WriteString(itoa(clients))
		html.WriteString(`</td></tr>`)
	}
	html.WriteString(`</table>`)

	SetHTML("dmsg-server-list", html.String())
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return itoa(int(bytes)) + "B"
	} else if bytes < 1024*1024 {
		return ftoa(float64(bytes)/1024) + "KB"
	} else if bytes < 1024*1024*1024 {
		return ftoa(float64(bytes)/(1024*1024)) + "MB"
	}
	return ftoa(float64(bytes)/(1024*1024*1024)) + "GB"
}

// updateAppsSection updates the Apps section in the sidebar (matching TypeScript apps.ts renderAppsUI)
func (a *App) updateAppsSection() {
	if len(a.appsData) == 0 {
		SetHTML("apps-list", `<div style="color:#666;font-size:11px;padding:8px;">No apps available</div>`)
		return
	}

	var html strings.Builder

	for _, app := range a.appsData {
		// Status indicator color and text (matching TypeScript STATUS_INFO)
		statusColor := "#666"
		statusBg := "rgba(136,136,136,0.2)"
		statusText := "Stopped"
		switch app.Status {
		case 1: // running
			statusColor = ColorOnlineBg
			statusBg = "rgba(0,217,165,0.2)"
			statusText = "Running"
		case 2: // errored
			statusColor = ColorOfflineBg
			statusBg = "rgba(233,69,96,0.2)"
			statusText = "Error"
		case 3: // starting
			statusColor = ColorUnknownBg
			statusBg = "rgba(255,209,102,0.2)"
			statusText = "Starting"
		}

		isRunning := app.Status == 1
		isClient := strings.Contains(app.Name, "-client")
		needsPK := isClient && (app.Name == "vpn-client" || app.Name == "skysocks-client")

		// Extract current server PK from args if present
		currentPK := ""
		for i, arg := range app.Args {
			if arg == "--srv" && i+1 < len(app.Args) {
				currentPK = app.Args[i+1]
				break
			}
		}

		// App item container
		html.WriteString(`<div class="app-item" data-app="`)
		html.WriteString(app.Name)
		html.WriteString(`" style="padding:6px 0;border-bottom:1px solid #0f3460;">`)

		// App header with name and status
		html.WriteString(`<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:4px;">`)
		html.WriteString(`<span style="font-size:11px;font-weight:600;">`)
		html.WriteString(app.Name)
		html.WriteString(`</span>`)
		html.WriteString(`<span style="font-size:9px;padding:2px 6px;border-radius:3px;color:`)
		html.WriteString(statusColor)
		html.WriteString(`;background:`)
		html.WriteString(statusBg)
		html.WriteString(`;">`)
		html.WriteString(statusText)
		html.WriteString(`</span></div>`)

		// App details: port and auto-start
		html.WriteString(`<div style="display:flex;justify-content:space-between;align-items:center;font-size:10px;color:#888;margin-bottom:4px;">`)
		html.WriteString(`<span>Port: `)
		html.WriteString(itoa(app.Port))
		html.WriteString(`</span>`)
		html.WriteString(`<label style="display:flex;align-items:center;gap:4px;cursor:pointer;">`)
		html.WriteString(`<input type="checkbox" `)
		if app.AutoStart {
			html.WriteString(`checked `)
		}
		html.WriteString(`onchange="appToggleAutoStart('`)
		html.WriteString(app.Name)
		html.WriteString(`', this.checked)" style="margin:0;">`)
		html.WriteString(`Auto-start</label></div>`)

		// Server PK input for client apps (vpn-client, skysocks-client)
		if needsPK {
			html.WriteString(`<div style="display:flex;gap:4px;margin-bottom:4px;">`)
			html.WriteString(`<input type="text" id="pk-`)
			html.WriteString(app.Name)
			html.WriteString(`" placeholder="Server PK" value="`)
			html.WriteString(currentPK)
			html.WriteString(`" style="flex:1;min-width:0;padding:3px 6px;font-size:10px;font-family:monospace;background:#1a1a2e;border:1px solid #0f3460;color:#eee;border-radius:2px;">`)
			html.WriteString(`<button onclick="appSetPK('`)
			html.WriteString(app.Name)
			html.WriteString(`')" style="padding:3px 6px;font-size:9px;background:#0f3460;color:#eee;border:none;border-radius:2px;cursor:pointer;">Set</button>`)
			html.WriteString(`</div>`)
		}

		// App actions: Start/Stop button
		html.WriteString(`<div style="display:flex;gap:4px;">`)
		if isRunning {
			html.WriteString(`<button onclick="appStop('`)
			html.WriteString(app.Name)
			html.WriteString(`')" style="flex:1;padding:4px 8px;font-size:10px;background:#e94560;color:#fff;border:none;border-radius:3px;cursor:pointer;">Stop</button>`)
		} else {
			html.WriteString(`<button onclick="appStart('`)
			html.WriteString(app.Name)
			html.WriteString(`')" style="flex:1;padding:4px 8px;font-size:10px;background:#00d9a5;color:#000;border:none;border-radius:3px;cursor:pointer;">Start</button>`)
		}
		html.WriteString(`</div>`)

		html.WriteString(`</div>`)
	}

	SetHTML("apps-list", html.String())
}

// updatePingResult updates the ping result display (matching TypeScript ping.ts performPing result display)
func (a *App) updatePingResult(resp *PingResponse, mode string) {
	resultEl := getElement("ping-result")
	if resultEl.IsNull() || resultEl.IsUndefined() {
		return
	}

	resultEl.Get("style").Set("display", "block")

	if resp.Error != "" || resp.Status == "error" {
		resultEl.Get("style").Set("background", "#e94560")
		resultEl.Get("style").Set("color", "#fff")
		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "Unknown error"
		}
		resultEl.Set("textContent", "Error: "+errMsg)
	} else if resp.Status == "timeout" {
		resultEl.Get("style").Set("background", "#ffd166")
		resultEl.Get("style").Set("color", "#000")
		resultEl.Set("textContent", "Timeout - no response received")
	} else if resp.Status == "success" {
		resultEl.Get("style").Set("background", "#00d9a5")
		resultEl.Get("style").Set("color", "#000")

		// Format latencies
		modeUpper := strings.ToUpper(mode)
		if resp.Mode != "" {
			modeUpper = strings.ToUpper(resp.Mode)
		}

		var statsText string
		if len(resp.Latencies) > 1 {
			statsText = modeUpper + ": min=" + ftoa(resp.MinMs) + "ms, avg=" + ftoa(resp.AvgMs) + "ms, max=" + ftoa(resp.MaxMs) + "ms"
			if resp.PacketLoss > 0 {
				statsText += ", loss=" + ftoa(resp.PacketLoss) + "%"
			}
		} else if len(resp.Latencies) == 1 {
			statsText = modeUpper + ": " + ftoa(resp.Latencies[0]) + "ms"
		} else {
			statsText = modeUpper + ": " + ftoa(resp.AvgMs) + "ms"
		}

		resultEl.Set("textContent", statsText)
	} else {
		resultEl.Get("style").Set("background", "#ffd166")
		resultEl.Get("style").Set("color", "#000")
		resultEl.Set("textContent", resp.Status)
	}
}

// showPingSection shows or hides the ping section (matching TypeScript ping.ts showPingSection)
func (a *App) showPingSection(show bool) {
	SetVisible("section-ping", show)
}

// updateLocalRouteVisibility updates local route checkbox visibility (matching TypeScript ping.ts updateLocalRouteVisibility)
func (a *App) updateLocalRouteVisibility() {
	useDmsg := GetChecked("ping-use-dmsg")
	localRouteRow := getElement("ping-local-route-row")
	if !localRouteRow.IsNull() && !localRouteRow.IsUndefined() {
		if useDmsg {
			localRouteRow.Get("style").Set("display", "none")
		} else {
			localRouteRow.Get("style").Set("display", "block")
		}
	}
}

// showAppMessage shows a message in the apps section (matching TypeScript apps.ts showAppMessage)
func (a *App) showAppMessage(msg string, msgType string) {
	msgEl := getElement("apps-message")
	if msgEl.IsNull() || msgEl.IsUndefined() {
		return
	}

	msgEl.Set("textContent", msg)
	msgEl.Get("style").Set("display", "block")

	if msgType == "success" {
		msgEl.Get("style").Set("color", "#00d9a5")
		msgEl.Get("style").Set("background", "rgba(0,217,165,0.1)")
	} else {
		msgEl.Get("style").Set("color", "#e94560")
		msgEl.Get("style").Set("background", "rgba(233,69,96,0.1)")
	}

	// Auto-hide after 3 seconds
	js.Global().Call("setTimeout", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		el := getElement("apps-message")
		if !el.IsNull() && !el.IsUndefined() {
			el.Get("style").Set("display", "none")
		}
		return nil
	}), 3000)
}
