//go:build js && wasm

package ui

import "strings"

// draw renders the entire scene
func (a *App) draw() {
	a.canvas.Clear(ColorBackground)

	if !a.dataLoaded {
		msg := "Loading transport data..."
		if a.loadError != "" {
			msg = "Error: " + a.loadError
		}
		a.canvas.Text(msg, a.canvas.Width()/2-100, a.canvas.Height()/2, ColorText, "14px sans-serif")
		return
	}

	// Calculate visible world bounds for culling
	pad := 50.0 / a.view.Scale
	a.visMinX = -a.view.OffsetX/a.view.Scale - pad
	a.visMinY = -a.view.OffsetY/a.view.Scale - pad
	a.visMaxX = (a.canvas.Width()-a.view.OffsetX)/a.view.Scale + pad
	a.visMaxY = (a.canvas.Height()-a.view.OffsetY)/a.view.Scale + pad

	// Phase 1: World-space rendering (canvas transform handles zoom scaling)
	a.canvas.Save()
	a.canvas.Translate(a.view.OffsetX, a.view.OffsetY)
	a.canvas.SetScale(a.view.Scale, a.view.Scale)

	// Draw group boundaries first (behind everything)
	a.drawGroupBoundaries()
	a.drawEdges()
	a.drawNodeCircles()

	a.canvas.Restore()

	// Phase 2: Screen-space rendering (text, overlays)
	a.drawNodeLabels()
	a.drawTooltip()
}

// drawGroupBoundaries draws cluster group boundaries
func (a *App) drawGroupBoundaries() {
	if !a.clusterByCountry && !a.clusterByIP {
		return
	}

	scale := a.view.Scale

	// Draw country group boundaries
	if a.clusterByCountry {
		for country, boundary := range a.countryBoundaries {
			if len(boundary.Points) < 3 {
				continue
			}
			color := getCountryColor(country)
			// Higher opacity for better visibility (TypeScript uses similar values)
			a.canvas.SetGlobalAlpha(0.25)
			a.canvas.FillPolygon(boundary.Points, color)
			a.canvas.SetGlobalAlpha(0.6)
			a.canvas.StrokePolygon(boundary.Points, 3, color)
			a.canvas.ResetGlobalAlpha()

			// Draw country label at top of boundary circle
			if scale > 0.15 {
				flag := countryToFlag(country)
				label := flag + " " + country
				if flag == "" {
					label = country
				}

				// Position label at top of circle
				labelX := boundary.CenterX
				labelY := boundary.CenterY - boundary.Radius - 10

				// Scale font size based on boundary radius
				fontSize := boundary.Radius * 0.15
				if fontSize < 12 {
					fontSize = 12
				}
				if fontSize > 24 {
					fontSize = 24
				}
				font := "bold " + ftoa(fontSize) + "px sans-serif"

				// Draw label with stroke for readability
				a.canvas.SetGlobalAlpha(0.9)
				a.canvas.StrokeText(label, labelX, labelY, "#000000", 3, font)
				a.canvas.Text(label, labelX, labelY, "#ffffff", font)
				a.canvas.ResetGlobalAlpha()
			}
		}
	}

	// Draw IP group boundaries (only for groups with nodes)
	if a.clusterByIP && a.ipGroupsEnabled {
		// First, count nodes per IP group
		// Count only VISIBLE nodes per IP group (respecting show/hide offline filter)
		ipGroupNodeCounts := make(map[int]int)
		for _, node := range a.graph.Nodes {
			if node.IPGroup > 0 && a.opts.ShowNodeStatus(node.Status) {
				ipGroupNodeCounts[node.IPGroup]++
			}
		}

		for group, boundary := range a.ipGroupBoundaries {
			// Skip empty IP groups
			if ipGroupNodeCounts[group] == 0 {
				continue
			}
			if len(boundary.Points) < 3 {
				continue
			}
			colorIdx := group % len(a.clusterColors)
			color := a.clusterColors[colorIdx]
			a.canvas.SetGlobalAlpha(0.15)
			a.canvas.FillPolygon(boundary.Points, color)
			a.canvas.SetGlobalAlpha(0.6)
			a.canvas.StrokePolygon(boundary.Points, 2, color)
			a.canvas.ResetGlobalAlpha()

			// Draw IP group label at center (only when zoomed in enough)
			if scale > 0.3 {
				label := "IP " + itoa(group)
				fontSize := boundary.Radius * 0.2
				if fontSize < 10 {
					fontSize = 10
				}
				if fontSize > 16 {
					fontSize = 16
				}
				font := ftoa(fontSize) + "px sans-serif"

				a.canvas.SetGlobalAlpha(0.7)
				a.canvas.Text(label, boundary.CenterX, boundary.CenterY-boundary.Radius+fontSize+5, color, font)
				a.canvas.ResetGlobalAlpha()
			}
		}
	}

	// Draw satellite orbit rings (for unknown-country nodes)
	if a.satelliteEnabled && a.orbitBaseRadius > 0 {
		a.canvas.SetGlobalAlpha(0.2)
		// Draw the orbit lanes
		for lane := 0; lane < OrbitLanes; lane++ {
			r := a.orbitBaseRadius + float64(lane)*OrbitLaneSpacing
			a.canvas.StrokeCircle(a.orbitCenter.X, a.orbitCenter.Y, r, 1.0, "#888888")
		}
		a.canvas.ResetGlobalAlpha()
	}
}

func (a *App) drawEdges() {
	hasSearch := len(a.searchMatches) > 0
	scale := a.view.Scale

	// Level of detail - use straight lines when zoomed out far for performance
	useCurves := scale > 0.3
	showShadows := scale > 0.5
	edgeCount := len(a.graph.Edges)

	// For very large graphs, skip some edges when zoomed out
	skipRatio := 1
	if edgeCount > 1000 && scale < 0.3 {
		skipRatio = 3 // Draw every 3rd edge
	} else if edgeCount > 500 && scale < 0.2 {
		skipRatio = 2
	}

	edgeIndex := 0
	for _, edge := range a.graph.Edges {
		edgeIndex++

		if edge.Hidden || !a.opts.ShowEdgeType(edge.Type) {
			continue
		}

		// Skip some edges for performance when zoomed out on large graphs
		if skipRatio > 1 && edgeIndex%skipRatio != 0 && !edge.IsLocalEdge && !edge.IsRoutePath {
			continue
		}

		// Hide DMSG connections if DMSG servers are not shown
		if edge.IsDMSGConnection && !a.showDMSGServers {
			continue
		}

		fromNode, ok := a.graph.Nodes[edge.From]
		if !ok {
			continue
		}
		toNode, ok := a.graph.Nodes[edge.To]
		if !ok {
			continue
		}

		// Don't filter DMSG server nodes by status
		if !fromNode.IsDMSGServer && !a.opts.ShowNodeStatus(fromNode.Status) {
			continue
		}
		if !toNode.IsDMSGServer && !a.opts.ShowNodeStatus(toNode.Status) {
			continue
		}

		// Edge hiding: when a node is hovered/selected, hide unconnected edges entirely
		if a.edgeVisibilityNode != "" && !hasSearch {
			if edge.From != a.edgeVisibilityNode && edge.To != a.edgeVisibilityNode {
				continue
			}
		}

		// World-space coordinates
		x1, y1 := fromNode.X, fromNode.Y
		x2, y2 := toNode.X, toNode.Y

		// Cull off-screen edges using world bounds
		if (x1 < a.visMinX && x2 < a.visMinX) || (x1 > a.visMaxX && x2 > a.visMaxX) ||
			(y1 < a.visMinY && y2 < a.visMinY) || (y1 > a.visMaxY && y2 > a.visMaxY) {
			continue
		}

		edgeColor := a.edgeColor(edge.Type)
		// Scale line width inversely with zoom - thinner when zoomed in, thicker when zoomed out
		// This matches vis-network behavior where edges stay visually consistent
		lineWidth := 1.0
		if scale > 1.0 {
			lineWidth = 0.5 // Thinner when zoomed in
		} else if scale < 0.3 {
			lineWidth = 1.5 // Slightly thicker when zoomed out for visibility
		}
		// Opacity: more visible when zoomed out, less when zoomed in
		opacity := 0.5
		if scale > 1.0 {
			opacity = 0.35 // Less opaque when zoomed in
		} else if scale < 0.3 {
			opacity = 0.7 // More visible when zoomed out
		}
		dashed := false
		isLocalEdge := edge.IsLocalEdge || fromNode.IsLocalVisor || toNode.IsLocalVisor

		// Route path edges - dashed magenta
		if edge.IsRoutePath {
			edgeColor = ColorRoutePath
			lineWidth = 2.0
			opacity = 0.7
			dashed = true
		}

		// DMSG connection edges - red, thin
		if edge.IsDMSGConnection {
			edgeColor = ColorDMSGConnection
			lineWidth = 0.5
			opacity = 0.4
		}

		// Local visor edges
		if isLocalEdge {
			edgeColor = ColorLocalEdge
			lineWidth = 3.0
			opacity = 1.0
		}

		// Local-only transports (dashed cyan)
		if edge.IsLocalOnly {
			dashed = true
		}

		// Search highlighting
		if hasSearch {
			fromMatch := a.searchMatches[edge.From]
			toMatch := a.searchMatches[edge.To]
			if !fromMatch && !toMatch {
				edgeColor = ColorEdgeDim
				opacity = 0.15
			} else {
				opacity = 1.0
				lineWidth = 2.0
			}
		}

		// Connected edges get full opacity when a node is focused
		if a.edgeVisibilityNode != "" && !hasSearch {
			lineWidth = 2.0
			opacity = 1.0
		}

		a.canvas.SetGlobalAlpha(opacity)

		// Add subtle shadow/glow for local edges only when zoomed in
		if showShadows && isLocalEdge {
			a.canvas.SetShadow(ColorLocalEdge, 5, 0, 0)
		}

		// Use straight lines when zoomed out for performance
		if useCurves {
			if dashed {
				a.canvas.DashedQuadraticCurve(x1, y1, x2, y2, lineWidth, edgeColor, 8, 4)
			} else {
				a.canvas.QuadraticCurve(x1, y1, x2, y2, lineWidth, edgeColor)
			}
		} else {
			if dashed {
				a.canvas.DashedLine(x1, y1, x2, y2, lineWidth, edgeColor, 8, 4)
			} else {
				a.canvas.Line(x1, y1, x2, y2, lineWidth, edgeColor)
			}
		}

		if showShadows && isLocalEdge {
			a.canvas.ClearShadow()
		}
		a.canvas.ResetGlobalAlpha()
	}
}

func (a *App) drawNodeCircles() {
	hasSearch := len(a.searchMatches) > 0
	scale := a.view.Scale

	// Level of detail based on zoom
	showShadows := scale > 0.4
	showBorders := scale > 0.2 || len(a.graph.Nodes) < 200

	for _, node := range a.graph.Nodes {
		// Skip DMSG server nodes based on visibility setting
		if node.IsDMSGServer && !a.showDMSGServers {
			continue
		}

		// Don't filter DMSG server nodes by status
		if !node.IsDMSGServer && !a.opts.ShowNodeStatus(node.Status) {
			continue
		}

		x, y := node.X, node.Y
		size := node.Size

		// Cull using world bounds
		if x+size < a.visMinX || x-size > a.visMaxX ||
			y+size < a.visMinY || y-size > a.visMaxY {
			continue
		}

		// Dim nodes not matching search
		if hasSearch && !a.searchMatches[node.ID] {
			a.canvas.SetGlobalAlpha(0.15)
		}

		bgColor, borderColor := a.nodeColors(node)

		// Satellite nodes render as satellite emoji (matching TypeScript grouping.ts)
		if node.IsSatellite && scale > 0.3 {
			fontSize := size * 2.5
			if fontSize > 24 {
				fontSize = 24
			}
			if fontSize < 14 {
				fontSize = 14
			}
			font := ftoa(fontSize) + "px sans-serif"

			// Add shadow/glow for selected/hovered satellites
			if showShadows && (node.IsSelected || node.IsHovered) {
				a.canvas.SetShadow(ColorSelected, 6, 0, 0)
			}

			// Draw satellite emoji centered at node position
			a.canvas.Text(SatelliteEmoji, x-fontSize*0.4, y+fontSize*0.35, "", font)

			if showShadows && (node.IsSelected || node.IsHovered) {
				a.canvas.ClearShadow()
			}

			if hasSearch && !a.searchMatches[node.ID] {
				a.canvas.ResetGlobalAlpha()
			}
			continue
		}

		// TypeScript behavior: when showFlags is true and node has country,
		// use 'text' shape (flag replaces dot) - except for local visor
		useTextShape := a.showFlags && scale > 0.5 && node.Country != "" && !node.IsDMSGServer && !node.IsLocalVisor

		if useTextShape {
			// Text shape mode: draw flag emoji as the node (no circle)
			flag := countryToFlag(node.Country)
			if flag != "" {
				// Size the flag based on node size
				fontSize := size * 2.0
				if fontSize > 28 {
					fontSize = 28
				}
				if fontSize < 12 {
					fontSize = 12
				}
				font := ftoa(fontSize) + "px sans-serif"

				// Add shadow/glow for selected/hovered
				if showShadows && (node.IsSelected || node.IsHovered) {
					a.canvas.SetShadow(ColorSelected, 6, 0, 0)
				}

				// Draw flag centered at node position
				a.canvas.Text(flag, x-fontSize*0.4, y+fontSize*0.35, "", font)

				if showShadows && (node.IsSelected || node.IsHovered) {
					a.canvas.ClearShadow()
				}
			}
		} else {
			// Dot shape mode: draw circle as usual
			// Shadow/glow effects only when zoomed in enough
			if showShadows {
				if node.IsLocalVisor {
					a.canvas.SetShadow(ColorLocalVisorBg, 8, 0, 0)
					a.canvas.FillCircle(x, y, size, bgColor)
					a.canvas.ClearShadow()
				} else if node.IsSelected || node.IsHovered {
					a.canvas.SetShadow(ColorSelected, 6, 0, 0)
					a.canvas.FillCircle(x, y, size, bgColor)
					a.canvas.ClearShadow()
				} else if node.IsDMSGServer {
					a.canvas.SetShadow(ColorDMSGServerBg, 5, 0, 0)
					a.canvas.FillCircle(x, y, size, bgColor)
					a.canvas.ClearShadow()
				} else {
					a.canvas.FillCircle(x, y, size, bgColor)
				}
			} else {
				a.canvas.FillCircle(x, y, size, bgColor)
			}

			// Border - skip when zoomed out far for performance
			if showBorders {
				borderWidth := 2.0
				if node.IsLocalVisor {
					borderWidth = 4.0
				} else if node.Status == StatusOffline {
					borderWidth = 3.0
				}
				a.canvas.StrokeCircle(x, y, size, borderWidth, borderColor)
			}
		}

		if hasSearch && !a.searchMatches[node.ID] {
			a.canvas.ResetGlobalAlpha()
		}
	}
}

// drawNodeLabels renders text labels in screen space (constant size regardless of zoom)
func (a *App) drawNodeLabels() {
	for _, node := range a.graph.Nodes {
		if !a.opts.ShowNodeStatus(node.Status) {
			continue
		}

		sx, sy := a.view.WorldToScreen(node.X, node.Y)
		screenSize := node.Size * a.view.Scale

		// Cull
		if sx < -50 || sx > a.canvas.Width()+50 ||
			sy < -50 || sy > a.canvas.Height()+50 {
			continue
		}

		// Dim labels for search misses
		hasSearch := len(a.searchMatches) > 0
		if hasSearch && !a.searchMatches[node.ID] {
			continue
		}

		// Local visor always shows "⬢ YOU" label with stroke for readability
		if node.IsLocalVisor {
			label := "\u2b22 YOU"
			font := "bold 13px sans-serif"
			lx, ly := sx+screenSize+4, sy+4
			a.canvas.StrokeText(label, lx, ly, "#000000", 3, font)
			a.canvas.Text(label, lx, ly, ColorLocalVisorBg, font)
		} else if a.opts.ShowLabels && a.view.Scale > 0.3 {
			label := node.Label
			if label == "" {
				label = shortPK(node.ID)
			}
			a.canvas.Text(label, sx+screenSize+4, sy+4, ColorTextDim, "10px sans-serif")
		} else if a.view.Scale > 1.0 {
			// Auto-show labels when zoomed in close
			a.canvas.Text(shortPK(node.ID), sx+screenSize+4, sy+4, ColorTextDim, "9px sans-serif")
		}
	}
}

// drawTooltip renders a tooltip near the hovered node in screen space
func (a *App) drawTooltip() {
	if a.hoveredNode == nil || a.hoveredNode == a.selectedNode {
		return
	}

	node := a.hoveredNode
	sx, sy := a.view.WorldToScreen(node.X, node.Y)

	lines := []string{shortPK(node.ID)}

	switch node.Status {
	case StatusOnline:
		lines = append(lines, "Online")
	case StatusOffline:
		lines = append(lines, "Offline")
	default:
		lines = append(lines, "Unknown")
	}

	lines = append(lines, "Transports: "+itoa(node.ConnectionCount))

	if node.Country != "" {
		lines = append(lines, "Country: "+node.Country)
	}
	if node.Version != "" {
		lines = append(lines, "Version: "+node.Version)
	}

	font := "11px sans-serif"
	lineHeight := 16.0
	padding := 8.0
	tooltipW := 0.0
	for _, line := range lines {
		w := a.canvas.MeasureText(line, font)
		if w > tooltipW {
			tooltipW = w
		}
	}
	tooltipW += padding * 2
	tooltipH := float64(len(lines))*lineHeight + padding*2

	tx := sx + node.Size*a.view.Scale + 10
	ty := sy - tooltipH/2
	if tx+tooltipW > a.canvas.Width()-10 {
		tx = sx - node.Size*a.view.Scale - tooltipW - 10
	}
	if ty < 10 {
		ty = 10
	}
	if ty+tooltipH > a.canvas.Height()-10 {
		ty = a.canvas.Height() - tooltipH - 10
	}

	a.canvas.RoundedRect(tx, ty, tooltipW, tooltipH, 4, ColorTooltipBg)
	a.canvas.StrokeRoundedRect(tx, ty, tooltipW, tooltipH, 4, 1, ColorTooltipBorder)

	textY := ty + padding + 12
	for _, line := range lines {
		a.canvas.Text(line, tx+padding, textY, ColorText, font)
		textY += lineHeight
	}
}

// nodeColors returns the background and border colors for a node
func (a *App) nodeColors(node *Node) (bg, border string) {
	// Selection/hover highlight
	if node.IsSelected || node.IsHovered {
		if node.IsLocalVisor {
			return ColorLocalVisorBg, "#ffffff"
		}
		if node.IsDMSGServer {
			return ColorDMSGServerBg, "#ffffff"
		}
		return ColorSelected, ColorHovered
	}

	if node.IsLocalVisor {
		return ColorLocalVisorBg, ColorLocalVisorBorder
	}

	if node.IsDMSGServer {
		return ColorDMSGServerBg, ColorDMSGServerBorder
	}

	if node.IsRouteDestination {
		return ColorRoutePath, "#ffffff"
	}

	// IP group clustering - use group color if enabled
	if a.clusterByIP && a.ipGroupsEnabled && node.IPGroup > 0 {
		colorIdx := node.IPGroup % len(a.clusterColors)
		groupColor := a.clusterColors[colorIdx]
		return groupColor, darkenColor(groupColor)
	}

	if a.opts.HighlightServices && node.HasServices {
		switch node.Service {
		case ServiceVPN:
			return ColorVPNBg, ColorVPNBorder
		case ServiceProxy:
			return ColorProxyBg, ColorProxyBorder
		}
	}

	switch node.Status {
	case StatusOnline:
		return ColorOnlineBg, ColorOnlineBorder
	case StatusOffline:
		return ColorOfflineBg, ColorOfflineBorder
	default:
		return ColorUnknownBg, ColorUnknownBorder
	}
}

// darkenColor returns a darker version of a hex color for borders
func darkenColor(hexColor string) string {
	switch hexColor {
	case "#00d9a5":
		return "#00b386"
	case "#00b4d8":
		return "#0096b4"
	case "#ffd166":
		return "#ccaa52"
	case "#e94560":
		return "#c13a50"
	case "#9f6efc":
		return "#7c3aed"
	default:
		return "#666666"
	}
}

func (a *App) edgeColor(t TransportType) string {
	switch t {
	case TransportSTCPR:
		return ColorSTCPR
	case TransportSUDPH:
		return ColorSUDPH
	case TransportDMSG:
		return ColorDMSG
	default:
		return ColorUnknownBg
	}
}

// countryToFlag converts a country code to a flag emoji
func countryToFlag(country string) string {
	if len(country) != 2 {
		return ""
	}
	country = strings.ToUpper(country)
	r1 := rune(country[0]) - 'A' + 0x1F1E6
	r2 := rune(country[1]) - 'A' + 0x1F1E6
	return string([]rune{r1, r2})
}

// getCountryColor returns a consistent color for a country
func getCountryColor(country string) string {
	if country == "" {
		return "#666666"
	}
	// Simple hash based on country code
	hash := int(country[0])*256 + int(country[1])
	colors := []string{
		"#00d9a5", "#00b4d8", "#ffd166", "#e94560", "#9f6efc",
		"#ff6b6b", "#4ecdc4", "#ffe66d", "#95e1d3", "#f38181",
	}
	return colors[hash%len(colors)]
}
