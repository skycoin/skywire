//go:build js && wasm

package ui

import (
	"math/rand"
	"strings"
)

// LoadData fetches all data and builds the graph
func (a *App) LoadData() {
	transports, err := a.fetcher.FetchTransports()
	if err != nil {
		a.loadError = err.Error()
		a.needsRedraw = true
		return
	}

	uptimes, _ := a.fetcher.FetchUptimes()
	services, _ := a.fetcher.FetchServices()

	// Count uptime totals (from UT data, not filtered to graph)
	a.uptimeOnlineCount = 0
	a.uptimeOfflineCount = 0
	for _, ut := range uptimes {
		if ut.Online {
			a.uptimeOnlineCount++
		} else {
			a.uptimeOfflineCount++
		}
	}

	isFirstLoad := !a.dataLoaded

	graph := ProcessTransports(transports, uptimes, services)

	if isFirstLoad {
		// DON'T randomize positions widely - vis-network starts nodes near center
		// and lets physics spread them naturally. We'll apply circle packing below
		// if clustering is enabled, or use small random offsets if not.
		// This prevents the "exploding outward" effect.
		for _, node := range graph.Nodes {
			// Small random offset near center (matching vis-network's initial positioning)
			node.X = (rand.Float64() - 0.5) * 100
			node.Y = (rand.Float64() - 0.5) * 100
		}
	} else {
		// Preserve existing node positions on refresh
		for id, newNode := range graph.Nodes {
			if oldNode, ok := a.graph.Nodes[id]; ok {
				newNode.X = oldNode.X
				newNode.Y = oldNode.Y
				newNode.VX = 0
				newNode.VY = 0
				newNode.IsPinned = oldNode.IsPinned
				newNode.IsSelected = oldNode.IsSelected
			}
		}
	}

	a.graph = graph
	a.dataLoaded = true
	a.loadError = ""
	a.needsRedraw = true

	// Update globe renderer's graph reference
	if a.globeRenderer != nil {
		a.globeRenderer.SetGraph(graph)
	}

	// Update sidebar
	a.updateSidebarStats()
	a.updateVisorList()

	// Fetch supplementary data (non-blocking failures)
	a.fetchHealthInfo()
	a.fetchLocalVisor()
	a.fetchTPSStatus()
	a.fetchDMSG()
	a.fetchIPGroups()
	a.fetchApps()

	// Apply circle-packing layout FIRST if clustering is enabled
	// This positions nodes in their clusters BEFORE physics runs
	// (matching TypeScript's arrangeNodesIntoGroups behavior)
	clustering := a.clusterByCountry || a.clusterByIP
	consoleLog("LoadData: clusterByCountry=" + boolStr(a.clusterByCountry) + ", clusterByIP=" + boolStr(a.clusterByIP) + ", clustering=" + boolStr(clustering))
	if clustering {
		a.applyCirclePackingLayout()
	} else {
		consoleLog("LoadData: clustering disabled, skipping applyCirclePackingLayout")
	}

	if isFirstLoad {
		if clustering {
			// When clustering is enabled, TypeScript uses stabilization: false
			// The circle packing already positioned nodes, so just do a quick settle
			// and fit immediately, then rely on boundary enforcement
			a.stabilizationLeft = 10 // Very short stabilization
			a.physicsEnabled = true
			// Fit immediately since nodes are already positioned by circle packing
			a.view.FitToGraph(a.graph, 50)
		} else {
			// No clustering: run full physics stabilization (matching vis-network: 100)
			a.stabilizationLeft = 100
			a.physicsEnabled = true
			// NOTE: FitToGraph is called AFTER stabilization completes (in app.go update loop)
			// This matches TypeScript's net.fit() call after stabilizationIterationsDone
		}
	} else {
		// Refresh: short stabilization to settle new nodes
		a.stabilizationLeft = 20
		a.physicsEnabled = true
	}
}

// fetchHealthInfo fetches health/auto-refresh info
func (a *App) fetchHealthInfo() {
	health, err := a.fetcher.FetchHealth()
	if err != nil {
		return
	}
	if health.AutoRefresh && health.NextRefreshIn > 0 {
		a.autoRefresh = true
		a.refreshInterval = health.NextRefreshIn
		a.refreshCountdown = health.NextRefreshIn
		SetVisible("refresh-bar", true)
		SetText("refresh-text", "Refresh in "+itoa(a.refreshCountdown)+"s")
		SetText("cache-info", "Auto-refresh in: "+itoa(a.refreshCountdown)+"s")
	}
}

// pollLocalVisor is a lightweight refresh of local visor data
func (a *App) pollLocalVisor() {
	lv, err := a.fetcher.FetchLocalVisor()
	if err != nil || lv.PubKey == "" {
		return
	}

	// Update data
	a.localVisorPK = lv.PubKey
	a.localVisorData = lv

	// Update sidebar stats
	SetText("local-visor-transports", itoa(len(lv.Transports)))
	SetText("local-visor-routes", itoa(len(lv.Routes)))

	// Calculate total traffic
	var totalSent, totalRecv int64
	for _, tp := range lv.Transports {
		totalSent += tp.SentBytes
		totalRecv += tp.RecvBytes
	}
	SetText("local-visor-sent", formatBytes(totalSent))
	SetText("local-visor-recv", formatBytes(totalRecv))

	// Update transport list
	a.updateLocalTransportList()

	// Update edge styling for local transports
	a.updateLocalEdgeStyling()

	a.needsRedraw = true
}

// fetchLocalVisor fetches local visor data
func (a *App) fetchLocalVisor() {
	lv, err := a.fetcher.FetchLocalVisor()
	if err != nil || lv.PubKey == "" {
		return
	}

	a.localVisorPK = lv.PubKey
	a.localVisorData = lv
	SetVisible("local-visor-section", true)
	SetText("local-visor-pk", lv.PubKey)

	// Show ping section and transport setup when visor is connected
	// (matching TypeScript ping.ts showPingSection and tps.ts visibility)
	a.showPingSection(lv.Connected)
	SetVisible("tps-section", lv.Connected)

	if lv.Connected {
		SetText("local-visor-status", "Connected")
		dot := getElement("local-visor-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOnlineBg)
		}
	} else {
		SetText("local-visor-status", "Disconnected")
		dot := getElement("local-visor-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOfflineBg)
		}
	}

	SetText("local-visor-transports", itoa(len(lv.Transports)))
	SetText("local-visor-routes", itoa(len(lv.Routes)))

	// Calculate total traffic
	var totalSent, totalRecv int64
	for _, tp := range lv.Transports {
		totalSent += tp.SentBytes
		totalRecv += tp.RecvBytes
	}
	SetText("local-visor-sent", formatBytes(totalSent))
	SetText("local-visor-recv", formatBytes(totalRecv))

	// Update transport list
	a.updateLocalTransportList()

	// Add route path edges
	a.addRoutePathEdges()

	// Mark local visor node in graph
	if node, ok := a.graph.Nodes[lv.PubKey]; ok {
		node.IsLocalVisor = true
		if node.Size < 20 {
			node.Size = 20
		}
		a.needsRedraw = true
	}

	// Mark local edges
	a.updateLocalEdgeStyling()
}

// addRoutePathEdges adds route path edges to the graph
func (a *App) addRoutePathEdges() {
	if a.localVisorData == nil {
		return
	}

	for _, route := range a.localVisorData.Routes {
		if route.NextHopPK != "" && route.DstPK != "" && route.NextHopPK != route.DstPK {
			// Create route destination node if it doesn't exist
			if _, exists := a.graph.Nodes[route.DstPK]; !exists {
				node := &Node{
					ID:                 route.DstPK,
					Size:               10,
					Status:             StatusUnknown,
					Label:              shortPK(route.DstPK),
					IsRouteDestination: true,
				}
				a.graph.AddNode(node)
				// Position near the next hop if it exists
				if hopNode, ok := a.graph.Nodes[route.NextHopPK]; ok {
					node.X = hopNode.X + (rand.Float64()-0.5)*100
					node.Y = hopNode.Y + (rand.Float64()-0.5)*100
				}
			}

			// Create route path edge
			edgeID := "route-" + shortPK(route.NextHopPK) + "-" + shortPK(route.DstPK)
			if _, exists := a.graph.Edges[edgeID]; !exists {
				edge := &Edge{
					ID:          edgeID,
					From:        route.NextHopPK,
					To:          route.DstPK,
					Type:        TransportDMSG,
					IsRoutePath: true,
				}
				a.graph.AddEdge(edge)
			}
		}
	}

	a.needsRedraw = true
}

// updateLocalEdgeStyling marks edges connected to the local visor
func (a *App) updateLocalEdgeStyling() {
	if a.localVisorData == nil || !a.localVisorData.Connected {
		return
	}

	localPK := a.localVisorData.PubKey
	localRemotes := make(map[string]bool)
	for _, tp := range a.localVisorData.Transports {
		localRemotes[tp.RemotePK] = true
	}

	for _, edge := range a.graph.Edges {
		involvesLocal := edge.From == localPK || edge.To == localPK
		remotePK := edge.From
		if edge.From == localPK {
			remotePK = edge.To
		}

		if involvesLocal && localRemotes[remotePK] {
			edge.IsLocalEdge = true
		}
	}
}

// fetchTPSStatus fetches TPS status
func (a *App) fetchTPSStatus() {
	tps, err := a.fetcher.FetchTPSStatus()
	if err != nil {
		return
	}

	SetVisible("section-tps", true)

	if tps.Running {
		SetText("tps-status", "Running")
		dot := getElement("tps-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOnlineBg)
		}
		if tps.TPSPK != "" {
			SetVisible("tps-pk-row", true)
			SetText("tps-pk", tps.TPSPK)
		}
	} else {
		SetText("tps-status", "Stopped")
		dot := getElement("tps-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOfflineBg)
		}
	}
}

// fetchDMSG fetches DMSG server data
func (a *App) fetchDMSG() {
	dmsg, err := a.fetcher.FetchDMSG()
	if err != nil {
		return
	}

	a.dmsgData = dmsg

	// Build entries lookup
	a.dmsgEntries = make(map[string]bool)
	for _, pk := range dmsg.Entries {
		a.dmsgEntries[pk] = true
	}

	// Update DMSG section in sidebar
	SetVisible("section-dmsg", true)
	SetText("dmsg-server-count", itoa(len(dmsg.Servers)))
	SetText("dmsg-entry-count", itoa(dmsg.EntriesCount))

	// Calculate total sessions
	totalSessions := 0
	for _, srv := range dmsg.Servers {
		totalSessions += srv.AvailableSessions
	}
	SetText("dmsg-total-sessions", itoa(totalSessions))

	// Add DMSG servers to graph
	a.addDMSGServersToGraph()
	a.updateDMSGServerList()
}

// addDMSGServersToGraph adds DMSG server nodes and connections to the graph
func (a *App) addDMSGServersToGraph() {
	if a.dmsgData == nil || !a.showDMSGServers {
		return
	}

	// Find max clients for size scaling
	maxClients := 1
	for _, srv := range a.dmsgData.Servers {
		if len(srv.Clients) > maxClients {
			maxClients = len(srv.Clients)
		}
	}

	// Build set of existing visor node IDs for edge targets
	visorNodeIDs := make(map[string]bool)
	for id := range a.graph.Nodes {
		if !strings.HasPrefix(id, "dmsg-srv-") {
			visorNodeIDs[id] = true
		}
	}

	for _, srv := range a.dmsgData.Servers {
		if srv.PK == "" {
			continue
		}

		nodeID := "dmsg-srv-" + srv.PK
		clientCount := len(srv.Clients)
		sessions := srv.AvailableSessions

		// Size formula matching visor nodes
		size := 5.0 + (float64(clientCount)/float64(maxClients))*25.0
		if size < 8 {
			size = 8
		}

		// Check if node already exists
		existingNode, exists := a.graph.Nodes[nodeID]
		if exists {
			// Update existing node
			existingNode.DMSGSessions = sessions
			existingNode.DMSGClients = clientCount
			existingNode.Size = size
		} else {
			// Create new DMSG server node
			node := &Node{
				ID:           nodeID,
				Size:         size,
				Status:       StatusUnknown,
				Country:      srv.Country,
				Label:        shortPK(srv.PK),
				IsDMSGServer: true,
				DMSGSessions: sessions,
				DMSGClients:  clientCount,
			}
			a.graph.AddNode(node)

			// Position near center initially
			node.X = (rand.Float64() - 0.5) * 200
			node.Y = (rand.Float64() - 0.5) * 200
		}

		// Add edges to connected clients that exist in graph
		for _, clientPK := range srv.Clients {
			if visorNodeIDs[clientPK] {
				edgeID := "dmsg-conn-" + shortPK(srv.PK) + "-" + shortPK(clientPK)
				if _, exists := a.graph.Edges[edgeID]; !exists {
					edge := &Edge{
						ID:               edgeID,
						From:             nodeID,
						To:               clientPK,
						Type:             TransportDMSG,
						IsDMSGConnection: true,
					}
					a.graph.AddEdge(edge)
				}
			}
		}
	}

	a.needsRedraw = true
}

// fetchIPGroups fetches IP group data
func (a *App) fetchIPGroups() {
	ipGroups, err := a.fetcher.FetchIPGroups()
	if err != nil {
		return
	}

	a.ipGroupsData = ipGroups
	a.ipGroupsEnabled = ipGroups.Enabled && ipGroups.TotalGroups > 1

	// Update sidebar
	if a.ipGroupsEnabled {
		SetVisible("section-ip-groups", true)
		SetText("ip-groups-count", itoa(ipGroups.TotalGroups))
	}

	// Assign IP groups to nodes
	for pk, groupNum := range ipGroups.Groups {
		if node, ok := a.graph.Nodes[pk]; ok {
			node.IPGroup = groupNum
		}
	}

	// Re-apply circle packing if enabled
	if a.clusterByIP {
		a.applyCirclePackingLayout()
	}

	a.needsRedraw = true
}

// fetchApps fetches apps from the local visor
func (a *App) fetchApps() {
	apps, err := a.fetcher.FetchApps()
	if err != nil {
		return
	}

	a.appsData = apps

	// Show apps section if we have any apps
	if len(apps) > 0 {
		SetVisible("section-apps", true)
		a.updateAppsSection()
	}
}
