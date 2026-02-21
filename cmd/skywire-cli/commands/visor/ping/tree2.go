// Package clivisor cmd/skywire-cli/commands/visor/ping-tree-tui.go
package ping

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/blang/semver/v4"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// TUI-specific flags
var (
	tuiVersion        string
	tuiMaxLevel       int
	tuiTimeout        time.Duration
	tuiSetupTimeout   time.Duration
	tuiTries          int
	tuiPcktSize       int
	tuiCacheTPD       string
	tuiCacheUT        string
	tuiCacheDMSG      string
	tuiCacheAge       int
	tuiTPDURL         string
	tuiUTURL          string
	tuiDMSGURL        string
	tuiOnlineOnly     bool
	tuiOutput         string
	tuiHops           uint
	tuiRetries        int
	tuiResume         bool
	tuiMaxAge         time.Duration
	tuiDryRun         bool
	tuiDmsgOnly       bool
	tuiDmsgPreCheck   bool
	tuiDmsgAllServers bool
	tuiUseTPS         bool
	tuiContinuous     bool
	tuiRecheckAge     time.Duration
	tuiRemoveTp       bool
	tuiRemoveRemoteTp bool
	tuiRemakeTp       bool
	tuiRemakeRemoteTp bool
	tuiConcurrency    int
)

func init() {
	pingTreeTUICmd.Flags().StringVarP(&tuiVersion, "version", "v", "", "filter by minimum version")
	pingTreeTUICmd.Flags().IntVarP(&tuiMaxLevel, "max-level", "l", 0, "maximum hop level (0 = unlimited)")
	pingTreeTUICmd.Flags().DurationVarP(&tuiTimeout, "timeout", "o", 30*time.Second, "timeout per ping attempt")
	pingTreeTUICmd.Flags().DurationVar(&tuiSetupTimeout, "setup-timeout", 30*time.Second, "timeout for route setup phase")
	pingTreeTUICmd.Flags().IntVarP(&tuiTries, "tries", "t", 1, "ping attempts per transport")
	pingTreeTUICmd.Flags().IntVarP(&tuiPcktSize, "size", "s", 2, "packet size in KB")
	pingTreeTUICmd.Flags().StringVar(&tuiCacheTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	pingTreeTUICmd.Flags().StringVar(&tuiCacheUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	pingTreeTUICmd.Flags().StringVar(&tuiCacheDMSG, "cfd", os.TempDir()+"/dmsg-clients.json", "DMSG clients cache file location")
	pingTreeTUICmd.Flags().IntVarP(&tuiCacheAge, "cfa", "m", 5, "update cache files if older than n minutes")
	pingTreeTUICmd.Flags().StringVar(&tuiTPDURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery URL")
	pingTreeTUICmd.Flags().StringVar(&tuiUTURL, "uturl", deployment.Prod.UptimeTracker, "uptime tracker URL")
	pingTreeTUICmd.Flags().StringVar(&tuiDMSGURL, "dmsgurl", deployment.Prod.DmsgDiscovery, "DMSG discovery URL")
	pingTreeTUICmd.Flags().BoolVarP(&tuiOnlineOnly, "online", "g", false, "only ping visors marked online in UT")
	pingTreeTUICmd.Flags().StringVarP(&tuiOutput, "output", "O", "", "output base filename (writes .json file)")
	pingTreeTUICmd.Flags().UintVar(&tuiHops, "hops", 0, "exact hop level to ping (0 = all levels)")
	pingTreeTUICmd.Flags().IntVar(&tuiRetries, "retries", 1, "retry attempts if ping fails")
	pingTreeTUICmd.Flags().BoolVarP(&tuiResume, "resume", "R", false, "resume from output file if it exists")
	pingTreeTUICmd.Flags().DurationVar(&tuiMaxAge, "max-age", 0, "re-ping entries older than this duration")
	pingTreeTUICmd.Flags().BoolVar(&tuiDryRun, "dry-run", false, "show tree structure without pinging")
	pingTreeTUICmd.Flags().BoolVar(&tuiDmsgOnly, "dmsg-only", false, "ping via DMSG servers instead of routes")
	pingTreeTUICmd.Flags().BoolVar(&tuiDmsgPreCheck, "dmsg", false, "pre-check visor reachability over DMSG before route ping")
	pingTreeTUICmd.Flags().BoolVar(&tuiDmsgAllServers, "dmsg-all-servers", false, "ping via all DMSG servers (not just first success)")
	pingTreeTUICmd.Flags().BoolVar(&tuiUseTPS, "tps", true, "verify/update transports via TPS (default: true)")
	pingTreeTUICmd.Flags().BoolVar(&tuiContinuous, "continuous", false, "run continuously, re-checking trees")
	pingTreeTUICmd.Flags().DurationVar(&tuiRecheckAge, "recheck-age", 24*time.Hour, "re-ping entries older than this in continuous mode")
	pingTreeTUICmd.Flags().BoolVar(&tuiRemoveTp, "remove-tp", false, "remove local transport if route ping fails")
	pingTreeTUICmd.Flags().BoolVar(&tuiRemoveRemoteTp, "remove-remote-tp", false, "request remote visor to remove transport if route ping fails")
	pingTreeTUICmd.Flags().BoolVar(&tuiRemakeTp, "remake-tp", false, "remake local transport after removing failed one (retry once)")
	pingTreeTUICmd.Flags().BoolVar(&tuiRemakeRemoteTp, "remake-remote-tp", false, "remake transport on remote side after failure (retry once)")
	pingTreeTUICmd.Flags().IntVarP(&tuiConcurrency, "concurrency", "c", 2, "max concurrent ping operations")

	RootCmd.AddCommand(pingTreeTUICmd)
}

var pingTreeTUICmd = &cobra.Command{
	Use:   "tree2",
	Short: "Ping visors via transport routes (scrollable TUI)",
	Long: `Ping visors via transport routes with a scrollable terminal UI.

This command uses a Bubble Tea-based TUI that allows scrolling through
results while the ping test is running.

Controls:
  ↑/k, ↓/j     Scroll up/down one line
  PgUp/PgDn    Scroll up/down one page
  Home/End     Go to top/bottom
  q/Ctrl+C     Quit

The display updates live while preserving your scroll position.`,
	Run: runPingTreeTUI,
}

// tuiTreeEntry represents a single entry in the tree
type tuiTreeEntry struct {
	tpID       string
	tpType     string
	remotePK   string
	level      int
	parentPK   string
	failed     bool
	removed    bool
	removeErr  string
	remade     bool
	remadeOnce bool
}

// tuiDmsgServerData tracks DMSG ping results via a specific server
type tuiDmsgServerData struct {
	serverPK    string
	pingSamples []float64
	pingErr     string
	phase       string // "pending", "ping", "done"
	timestamp   time.Time
}

// tuiLatencyData tracks timing for each transport ping
type tuiLatencyData struct {
	tpID           string
	tpType         string
	from           string
	to             string
	gatewayPK      string // First-hop visor for level 2+ entries
	level          int
	calcTimeMs     float64
	setupTimeMs    float64
	pingSamples    []float64
	calcErr        string
	setupErr       string
	pingErr        string
	phase          string // "pending", "calc", "setup", "ping", "done"
	timestamp      time.Time
	lastSuccess    time.Time
	stale          bool
	dmsgReachable  bool
	dmsgSkipReason string
	dmsgServers    []*tuiDmsgServerData
}

// tuiSavedState represents the saved state for resume functionality
type tuiSavedState struct {
	LocalPK    string                 `json:"local_pk"`
	StartTime  string                 `json:"start_time"`
	UpdateTime string                 `json:"update_time"`
	Entries    []tuiSavedEntry        `json:"entries"`
	Settings   map[string]interface{} `json:"settings"`
}

// tuiDmsgServerSavedEntry represents DMSG ping results via a specific server
type tuiDmsgServerSavedEntry struct {
	ServerPK    string    `json:"server_pk"`
	PingSamples []float64 `json:"ping_samples,omitempty"`
	AvgLatency  float64   `json:"avg_latency_ms,omitempty"`
	PingErr     string    `json:"ping_err,omitempty"`
	Timestamp   string    `json:"timestamp,omitempty"`
}

// tuiSavedEntry represents a single transport ping entry in saved state
type tuiSavedEntry struct {
	TpID           string                    `json:"tp_id"`
	TpType         string                    `json:"tp_type"`
	RemotePK       string                    `json:"remote_pk"`
	ParentPK       string                    `json:"parent_pk"`
	GatewayPK      string                    `json:"gateway_pk,omitempty"`
	Level          int                       `json:"level"`
	CalcTimeMs     float64                   `json:"calc_time_ms,omitempty"`
	SetupTimeMs    float64                   `json:"setup_time_ms,omitempty"`
	PingSamples    []float64                 `json:"ping_samples,omitempty"`
	AvgLatency     float64                   `json:"avg_latency_ms,omitempty"`
	CalcErr        string                    `json:"calc_err,omitempty"`
	SetupErr       string                    `json:"setup_err,omitempty"`
	PingErr        string                    `json:"ping_err,omitempty"`
	Timestamp      string                    `json:"timestamp,omitempty"`
	LastSuccess    string                    `json:"last_success,omitempty"`
	Phase          string                    `json:"phase"`
	Stale          bool                      `json:"stale,omitempty"`
	DmsgServers    []tuiDmsgServerSavedEntry `json:"dmsg_servers,omitempty"`
	DmsgReachable  bool                      `json:"dmsg_reachable,omitempty"`
	DmsgSkipReason string                    `json:"dmsg_skip_reason,omitempty"`
}

// pingTreeModel is the Bubble Tea model for the scrollable ping tree
type pingTreeModel struct {
	viewport   viewport.Model
	content    string
	ready      bool
	width      int
	height     int
	quitting   bool
	autoScroll bool

	// Ping state
	ctx        context.Context
	cancel     context.CancelFunc
	grpcClient *rpcgrpc.PingClient
	rpcClient  visor.API
	localPK    string
	adjacency  map[string][]treeNeighbor
	localTps   []*visor.TransportSummary

	// Filter state
	passesFilter func(string) bool
	onlineSet    map[string]bool

	// DMSG state
	visorDmsgServers  map[string][]string // visorPK -> []serverPK
	dmsgClientsLoaded bool

	// Data
	entries     []tuiTreeEntry
	entriesMu   *sync.RWMutex
	latencies   map[string]*tuiLatencyData
	latenciesMu *sync.RWMutex
	pingedTpIDs map[string]bool // Already pinged transport IDs

	// Local transport tracking
	localTpIDs      map[string]bool
	localTpByRemote map[string]treeNeighbor

	// Stats
	totalVisors  int
	pingedVisors int
	failedVisors int
	startTime    time.Time

	// Status messages
	statusMu  *sync.RWMutex
	statusMsg string

	// Goroutine tracking
	pingWg *sync.WaitGroup
}

// Messages for Bubble Tea
type tickMsg time.Time

func (m pingTreeModel) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m pingTreeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			m.cancel()
			// Save state before quitting
			m.saveResults()
			return m, tea.Quit
		case "home", "g":
			m.autoScroll = false
			m.viewport.GotoTop()
		case "end", "G":
			m.autoScroll = true
			m.viewport.GotoBottom()
		case "up", "k":
			m.autoScroll = false
		case "down", "j":
			if m.viewport.AtBottom() {
				m.autoScroll = true
			}
		case "pgup", "ctrl+u":
			m.autoScroll = false
		case "pgdown", "ctrl+d":
			if m.viewport.AtBottom() {
				m.autoScroll = true
			}
		}

	case tea.WindowSizeMsg:
		headerHeight := 4
		footerHeight := 2
		verticalMarginHeight := headerHeight + footerHeight

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.viewport.SetContent(m.content)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.content = m.renderTreeContent()
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.content)
		if m.autoScroll || wasAtBottom {
			m.viewport.GotoBottom()
		}
		cmds = append(cmds, tickCmd())
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m pingTreeModel) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}
	if !m.ready {
		return "Initializing...\n"
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	header := headerStyle.Render("Ping Over Routes - Scrollable Tree View")

	elapsed := time.Since(m.startTime).Truncate(time.Second)
	statsLine := fmt.Sprintf("Visors: %d/%d pinged, %d failed | Elapsed: %s",
		m.pingedVisors, m.totalVisors, m.failedVisors, elapsed)

	m.statusMu.RLock()
	status := m.statusMsg
	m.statusMu.RUnlock()
	if status != "" {
		statsLine += " | " + status
	}

	scrollPercent := m.viewport.ScrollPercent() * 100
	scrollIndicator := fmt.Sprintf("%.0f%%", scrollPercent)
	if m.autoScroll {
		scrollIndicator += " [auto]"
	}

	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	footer := footerStyle.Render(fmt.Sprintf(
		"↑/↓ scroll | PgUp/PgDn page | Home/End top/bottom | q quit | %s",
		scrollIndicator,
	))

	return fmt.Sprintf("%s\n%s\n%s\n%s",
		header,
		statsLine,
		m.viewport.View(),
		footer,
	)
}

// setStatus updates the status message
func (m *pingTreeModel) setStatus(msg string) {
	m.statusMu.Lock()
	m.statusMsg = msg
	m.statusMu.Unlock()
}

// renderTreeContent generates the tree display string using pterm tree
func (m *pingTreeModel) renderTreeContent() string {
	m.entriesMu.RLock()
	entries := make([]tuiTreeEntry, len(m.entries))
	copy(entries, m.entries)
	m.entriesMu.RUnlock()

	if len(entries) == 0 {
		return "Discovering network topology...\n"
	}

	var sb strings.Builder

	// Column header using pterm tree format - matches graph.go/tree format
	// Column widths: remotePK(64) tpID(36) calc(1) setup(9) pings(8 each) avg(9)
	labelParts := []string{
		fmt.Sprintf("%-64s", pterm.Gray("edge")),
		fmt.Sprintf("%-36s", pterm.Green("tpid")),
		pterm.Gray("-"), // calc column
		fmt.Sprintf("%9s", pterm.Gray("setup")),
	}
	// Ping columns: "ping" in first slot, ".....ms" in rest - each 8 chars right-aligned
	labelParts = append(labelParts, fmt.Sprintf("%8s", pterm.Gray("ping")))
	for i := 1; i < tuiTries; i++ {
		labelParts = append(labelParts, fmt.Sprintf("%8s", pterm.Gray(".....ms")))
	}
	labelParts = append(labelParts, fmt.Sprintf("%9s", pterm.Gray("avg")))
	if tuiDmsgPreCheck || tuiDmsgOnly {
		labelParts = append(labelParts, pterm.Gray("dmsg"))
	}
	labelRow := strings.Join(labelParts, " ")

	labelTree := pterm.TreeNode{
		Text: pterm.Gray("edge"),
		Children: []pterm.TreeNode{
			{Text: labelRow},
		},
	}
	treeStr, _ := pterm.DefaultTree.WithRoot(labelTree).Srender() //nolint:errcheck
	sb.WriteString(treeStr)
	sb.WriteString("\n")

	// Group entries by level
	entriesByLevel := make(map[int][]tuiTreeEntry)
	for _, entry := range entries {
		entriesByLevel[entry.level] = append(entriesByLevel[entry.level], entry)
	}

	maxLevel := 0
	for lvl := range entriesByLevel {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}

	// Update stats
	pinged := 0
	failed := 0
	for _, entry := range entries {
		m.latenciesMu.RLock()
		data := m.latencies[entry.tpID]
		m.latenciesMu.RUnlock()
		if data != nil && data.phase == "done" {
			pinged++
			if entry.failed || data.pingErr != "" || data.setupErr != "" {
				failed++
			}
		}
	}
	m.pingedVisors = pinged
	m.failedVisors = failed
	m.totalVisors = len(entries)

	// Render Level 1 (direct transports)
	level1Entries, hasLevel1 := entriesByLevel[1]
	if hasLevel1 && len(level1Entries) > 0 {
		sb.WriteString("=== Level 1 (direct transports) ===\n")

		// Sort entries: successful by latency first, then failed
		m.sortEntriesByLatency(level1Entries)

		// Separate success and failure entries
		var successEntries, failedEntries []tuiTreeEntry
		for _, entry := range level1Entries {
			if entry.failed {
				failedEntries = append(failedEntries, entry)
			} else {
				m.latenciesMu.RLock()
				data := m.latencies[entry.tpID]
				m.latenciesMu.RUnlock()
				if data != nil && (data.pingErr != "" || data.setupErr != "") {
					failedEntries = append(failedEntries, entry)
				} else {
					successEntries = append(successEntries, entry)
				}
			}
		}

		// Render success tree
		if len(successEntries) > 0 {
			var children []pterm.TreeNode
			for _, entry := range successEntries {
				children = append(children, m.buildTreeNodeWithDmsg(entry))
			}
			rootTree := pterm.TreeNode{
				Text:     pterm.Cyan(m.localPK) + pterm.Gray(" (local)"),
				Children: children,
			}
			treeStr, _ := pterm.DefaultTree.WithRoot(rootTree).Srender() //nolint:errcheck
			sb.WriteString(treeStr)
		}

		// Render failure tree
		if len(failedEntries) > 0 {
			var children []pterm.TreeNode
			for _, entry := range failedEntries {
				children = append(children, m.buildTreeNodeWithDmsg(entry))
			}
			failedTree := pterm.TreeNode{
				Text:     pterm.Cyan(m.localPK) + pterm.Red(" (failures)"),
				Children: children,
			}
			treeStr, _ := pterm.DefaultTree.WithRoot(failedTree).Srender() //nolint:errcheck
			sb.WriteString(treeStr)
		}
	}

	// Render Level 2+ trees: separate tree per parent
	for lvl := 2; lvl <= maxLevel; lvl++ {
		levelEntries, ok := entriesByLevel[lvl]
		if !ok || len(levelEntries) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("\n=== Level %d ===\n", lvl))

		// Group entries by parent
		entriesByParent := make(map[string][]tuiTreeEntry)
		for _, entry := range levelEntries {
			entriesByParent[entry.parentPK] = append(entriesByParent[entry.parentPK], entry)
		}

		// Sort parents by their best child latency
		type parentInfo struct {
			pk          string
			bestLatency float64
		}
		var sortedParents []parentInfo
		for parentPK, children := range entriesByParent {
			best := float64(-1)
			for _, child := range children {
				lat := m.getAvgLatency(child.tpID)
				if lat >= 0 && (best < 0 || lat < best) {
					best = lat
				}
			}
			sortedParents = append(sortedParents, parentInfo{pk: parentPK, bestLatency: best})
		}
		// Sort: lowest latency first, -1 (no data) last
		for i := 0; i < len(sortedParents)-1; i++ {
			for j := i + 1; j < len(sortedParents); j++ {
				swap := false
				if sortedParents[i].bestLatency < 0 && sortedParents[j].bestLatency >= 0 {
					swap = true
				} else if sortedParents[i].bestLatency >= 0 && sortedParents[j].bestLatency >= 0 {
					if sortedParents[j].bestLatency < sortedParents[i].bestLatency {
						swap = true
					}
				}
				if swap {
					sortedParents[i], sortedParents[j] = sortedParents[j], sortedParents[i]
				}
			}
		}

		// Render tree for each parent
		for _, parent := range sortedParents {
			children := entriesByParent[parent.pk]
			m.sortEntriesByLatency(children)

			// Separate success and failure
			var successEntries, failedLevelEntries []tuiTreeEntry
			for _, entry := range children {
				if entry.failed {
					failedLevelEntries = append(failedLevelEntries, entry)
				} else {
					m.latenciesMu.RLock()
					data := m.latencies[entry.tpID]
					m.latenciesMu.RUnlock()
					if data != nil && (data.pingErr != "" || data.setupErr != "") {
						failedLevelEntries = append(failedLevelEntries, entry)
					} else {
						successEntries = append(successEntries, entry)
					}
				}
			}

			// Build parent text with latency info
			parentLatStr := ""
			if parent.bestLatency >= 0 {
				parentLatStr = pterm.Gray(fmt.Sprintf(" (%.1fms)", parent.bestLatency))
			}

			// Render success tree for this parent
			if len(successEntries) > 0 {
				var treeChildren []pterm.TreeNode
				for _, entry := range successEntries {
					treeChildren = append(treeChildren, m.buildTreeNodeWithDmsg(entry))
				}
				parentTree := pterm.TreeNode{
					Text:     pterm.Cyan(parent.pk) + parentLatStr,
					Children: treeChildren,
				}
				treeStr, _ := pterm.DefaultTree.WithRoot(parentTree).Srender() //nolint:errcheck
				sb.WriteString(treeStr)
			}

			// Render failure tree for this parent
			if len(failedLevelEntries) > 0 {
				var treeChildren []pterm.TreeNode
				for _, entry := range failedLevelEntries {
					treeChildren = append(treeChildren, m.buildTreeNodeWithDmsg(entry))
				}
				failedTree := pterm.TreeNode{
					Text:     pterm.Cyan(parent.pk) + pterm.Red(" (failures)") + parentLatStr,
					Children: treeChildren,
				}
				treeStr, _ := pterm.DefaultTree.WithRoot(failedTree).Srender() //nolint:errcheck
				sb.WriteString(treeStr)
			}
		}
	}

	return sb.String()
}

// buildTreeNodeWithDmsg creates a tree node for a transport entry with DMSG server children
func (m *pingTreeModel) buildTreeNodeWithDmsg(entry tuiTreeEntry) pterm.TreeNode {
	node := pterm.TreeNode{Text: m.formatEntryForTree(entry)}

	// Add DMSG server children if available
	if tuiDmsgPreCheck || tuiDmsgOnly {
		m.latenciesMu.RLock()
		data := m.latencies[entry.tpID]
		m.latenciesMu.RUnlock()

		if data != nil && len(data.dmsgServers) > 0 {
			for _, serverData := range data.dmsgServers {
				node.Children = append(node.Children, pterm.TreeNode{
					Text: m.formatDmsgServerEntry(serverData),
				})
			}
		}
	}

	return node
}

// formatDmsgServerEntry formats a DMSG server entry for tree display
// Matches the format used in graph.go/tree command
func (m *pingTreeModel) formatDmsgServerEntry(serverData *tuiDmsgServerData) string {
	if serverData == nil {
		return pterm.Gray("...")
	}

	// Helper to truncate error messages
	truncateErr := func(err string, maxLen int) string {
		if len(err) <= maxLen {
			return err
		}
		return err[:maxLen-3] + "..."
	}

	// Format aligned with transport entries:
	// serverPK(64) | (dmsg)(34) | calc(-) | setup(-) | pings(8 each) | avg(9)
	var pingsStr, avgStr string

	if serverData.pingErr != "" {
		pingsStr = truncateErr(serverData.pingErr, 12)
		avgStr = fmt.Sprintf("%9s", "-")
	} else if len(serverData.pingSamples) > 0 {
		// Format ping samples
		var pingParts []string
		var pingSum float64
		for _, p := range serverData.pingSamples {
			pingParts = append(pingParts, fmt.Sprintf("%8s", fmt.Sprintf("%.1fms", p)))
			pingSum += p
		}
		pingsStr = strings.Join(pingParts, " ")
		avgPing := pingSum / float64(len(serverData.pingSamples))
		avgStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", avgPing))
	} else if serverData.phase != "done" {
		pingsStr = fmt.Sprintf("%8s", "...")
		avgStr = fmt.Sprintf("%9s", "...")
	} else {
		pingsStr = fmt.Sprintf("%8s", "-")
		avgStr = fmt.Sprintf("%9s", "-")
	}

	// Build line aligned with transport entries:
	// serverPK (64 chars) | "(dmsg)" padded to 34 chars (2 less to compensate for tree indent) | "-" for calc | "-" padded to 9 for setup | pings | avg | timestamp
	dmsgLabel := fmt.Sprintf("%-34s", "(dmsg)")
	calcStr := "-"
	setupStr := fmt.Sprintf("%9s", "-")

	// Timestamp (grayed, at the end) - same format as transport entries
	var tsStr string
	if serverData.phase == "done" && !serverData.timestamp.IsZero() {
		tsStr = pterm.Gray(fmt.Sprintf(" %s", serverData.timestamp.Format("2006-01-02 15:04:05")))
	}

	// Use magenta color for DMSG servers to distinguish from transports
	var text string
	if serverData.pingErr != "" {
		// Red text for failed DMSG pings
		text = pterm.Red(fmt.Sprintf("%s %s %s %s %s %s",
			serverData.serverPK, dmsgLabel, calcStr, setupStr, pingsStr, avgStr)) + tsStr
	} else {
		// Magenta server PK, gray labels
		text = fmt.Sprintf("%s %s %s %s %s %s",
			pterm.Magenta(serverData.serverPK), pterm.Gray(dmsgLabel), pterm.Gray(calcStr), pterm.Gray(setupStr), pingsStr, avgStr) + tsStr
	}

	return text
}

// sortEntriesByLatency sorts entries by latency (lowest first, failures last)
func (m *pingTreeModel) sortEntriesByLatency(entries []tuiTreeEntry) {
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			avgI := m.getAvgLatency(entries[i].tpID)
			avgJ := m.getAvgLatency(entries[j].tpID)
			if entries[i].failed && !entries[j].failed {
				entries[i], entries[j] = entries[j], entries[i]
			} else if !entries[i].failed && !entries[j].failed {
				if avgI < 0 && avgJ >= 0 {
					entries[i], entries[j] = entries[j], entries[i]
				} else if avgI >= 0 && avgJ >= 0 && avgJ < avgI {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	}
}

// formatEntryForTree formats an entry for pterm tree display
// Matches the format used in graph.go/tree command
func (m *pingTreeModel) formatEntryForTree(entry tuiTreeEntry) string {
	m.latenciesMu.RLock()
	data := m.latencies[entry.tpID]
	m.latenciesMu.RUnlock()

	pk := entry.remotePK
	tpID := entry.tpID

	// Helper to truncate error messages
	truncateErr := func(err string, maxLen int) string {
		if len(err) <= maxLen {
			return err
		}
		return err[:maxLen-3] + "..."
	}

	// Color transport ID by type
	formatTpID := func(id, tpType string) string {
		switch tpType {
		case "stcpr":
			return pterm.Green(id)
		case "sudph":
			return pterm.Blue(id)
		case "dmsg":
			return pterm.Yellow(id)
		default:
			return id
		}
	}

	if data == nil {
		return fmt.Sprintf("%s %s ... %9s %8s %9s", pk, tpID, "...", "...", "...")
	}

	// Determine failure types
	earlyFailure := data.calcErr != "" || data.setupErr != ""
	pingFailure := data.pingErr != "" && !earlyFailure

	// Calc time/error (minimal width, expands for errors/values)
	var calcStr string
	if data.calcErr != "" {
		calcStr = truncateErr(data.calcErr, 9)
	} else if data.calcTimeMs > 0 {
		calcStr = fmt.Sprintf("%.1fms", data.calcTimeMs)
	} else if data.phase == "pending" || data.phase == "calc" {
		calcStr = "..."
	} else {
		calcStr = "-"
	}

	// Setup time/error (9 chars, right-aligned)
	var setupStr string
	if data.setupErr != "" {
		setupStr = fmt.Sprintf("%9s", truncateErr(data.setupErr, 9))
	} else if data.setupTimeMs > 0 {
		setupStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", data.setupTimeMs))
	} else if data.phase == "pending" || data.phase == "calc" || data.phase == "setup" {
		setupStr = fmt.Sprintf("%9s", "...")
	} else {
		setupStr = fmt.Sprintf("%9s", "-")
	}

	// Ping times/error - show all samples (space-separated, each 8 chars for alignment)
	var pingsStr string
	if data.pingErr != "" {
		pingsStr = truncateErr(data.pingErr, 12)
	} else if len(data.pingSamples) > 0 {
		var pingParts []string
		for _, p := range data.pingSamples {
			pingParts = append(pingParts, fmt.Sprintf("%8s", fmt.Sprintf("%.1fms", p)))
		}
		pingsStr = strings.Join(pingParts, " ")
	} else if data.phase != "done" {
		pingsStr = fmt.Sprintf("%8s", "...")
	} else {
		pingsStr = fmt.Sprintf("%8s", "-")
	}

	// Average ping time (9 chars, right-aligned)
	var totalStr string
	if !earlyFailure && !pingFailure && len(data.pingSamples) > 0 {
		var pingSum float64
		for _, p := range data.pingSamples {
			pingSum += p
		}
		avgPing := pingSum / float64(len(data.pingSamples))
		totalStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", avgPing))
	} else {
		totalStr = fmt.Sprintf("%9s", "-")
	}

	// Timestamp (grayed, at the end)
	var tsStr string
	if data.phase == "done" && !data.timestamp.IsZero() {
		tsStr = pterm.Gray(fmt.Sprintf(" %s", data.timestamp.Format("2006-01-02 15:04:05")))
	}

	// Removal status (for failed entries)
	var removeStr string
	if entry.removed {
		removeStr = pterm.Green(" [REMOVED]")
	} else if entry.removeErr != "" {
		removeStr = pterm.Yellow(" [rm err: " + truncateErr(entry.removeErr, 20) + "]")
	}
	if entry.remade {
		removeStr += pterm.Cyan(" [REMADE]")
	}

	// Build the line: remotePK tpID calc setup pings... avg timestamp [removal_status]
	tpIDFormatted := formatTpID(tpID, entry.tpType)

	// Format the entire line based on failure status
	var text string
	if earlyFailure {
		// Red background with white text for early failures
		line := fmt.Sprintf("%s %s %s %s %s %s",
			pk, tpID, calcStr, setupStr, pingsStr, totalStr)
		text = pterm.BgRed.Sprint(pterm.White(line)) + tsStr + removeStr
	} else if pingFailure {
		// Red text for ping failures
		text = pterm.Red(fmt.Sprintf("%s %s %s %s %s %s",
			pk, tpID, calcStr, setupStr, pingsStr, totalStr)) + tsStr + removeStr
	} else {
		// Normal formatting with colored tpID
		text = fmt.Sprintf("%s %s %s %s %s %s",
			pk, tpIDFormatted, calcStr, setupStr, pingsStr, totalStr) + tsStr + removeStr
	}

	return text
}

// getAvgLatency returns average ping latency for a transport
func (m *pingTreeModel) getAvgLatency(tpID string) float64 {
	m.latenciesMu.RLock()
	defer m.latenciesMu.RUnlock()

	data := m.latencies[tpID]
	if data == nil || len(data.pingSamples) == 0 {
		return -1
	}

	var sum float64
	for _, s := range data.pingSamples {
		sum += s
	}
	return sum / float64(len(data.pingSamples))
}

// saveResults saves the current state to JSON file
func (m *pingTreeModel) saveResults() {
	if tuiOutput == "" {
		return
	}

	m.entriesMu.RLock()
	entries := make([]tuiTreeEntry, len(m.entries))
	copy(entries, m.entries)
	m.entriesMu.RUnlock()

	m.latenciesMu.RLock()
	latencies := make(map[string]*tuiLatencyData)
	for k, v := range m.latencies {
		latencies[k] = v
	}
	m.latenciesMu.RUnlock()

	state := tuiSavedState{
		LocalPK:    m.localPK,
		StartTime:  m.startTime.Format(time.RFC3339),
		UpdateTime: time.Now().Format(time.RFC3339),
		Settings: map[string]interface{}{
			"tries":   tuiTries,
			"timeout": tuiTimeout.String(),
			"version": tuiVersion,
		},
	}

	for _, entry := range entries {
		data := latencies[entry.tpID]
		if data == nil {
			continue
		}

		var avgLatency float64
		if len(data.pingSamples) > 0 {
			for _, s := range data.pingSamples {
				avgLatency += s
			}
			avgLatency /= float64(len(data.pingSamples))
		}

		savedEntry := tuiSavedEntry{
			TpID:           entry.tpID,
			TpType:         data.tpType,
			RemotePK:       entry.remotePK,
			ParentPK:       entry.parentPK,
			GatewayPK:      data.gatewayPK,
			Level:          entry.level,
			CalcTimeMs:     data.calcTimeMs,
			SetupTimeMs:    data.setupTimeMs,
			PingSamples:    data.pingSamples,
			AvgLatency:     avgLatency,
			CalcErr:        data.calcErr,
			SetupErr:       data.setupErr,
			PingErr:        data.pingErr,
			Phase:          data.phase,
			Stale:          data.stale,
			DmsgReachable:  data.dmsgReachable,
			DmsgSkipReason: data.dmsgSkipReason,
		}

		if !data.timestamp.IsZero() {
			savedEntry.Timestamp = data.timestamp.Format(time.RFC3339)
		}
		if !data.lastSuccess.IsZero() {
			savedEntry.LastSuccess = data.lastSuccess.Format(time.RFC3339)
		}

		// Save DMSG server results
		for _, srv := range data.dmsgServers {
			var avgDmsg float64
			if len(srv.pingSamples) > 0 {
				for _, s := range srv.pingSamples {
					avgDmsg += s
				}
				avgDmsg /= float64(len(srv.pingSamples))
			}
			savedEntry.DmsgServers = append(savedEntry.DmsgServers, tuiDmsgServerSavedEntry{
				ServerPK:    srv.serverPK,
				PingSamples: srv.pingSamples,
				AvgLatency:  avgDmsg,
				PingErr:     srv.pingErr,
				Timestamp:   srv.timestamp.Format(time.RFC3339),
			})
		}

		state.Entries = append(state.Entries, savedEntry)
	}

	jsonData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}

	jsonFile := tuiOutput + ".json"
	_ = os.WriteFile(jsonFile, jsonData, 0600) //nolint:errcheck,gosec
}

// loadSavedState loads previous state from JSON file for resume
func (m *pingTreeModel) loadSavedState() bool {
	if !tuiResume || tuiOutput == "" {
		return false
	}

	resumeFile := tuiOutput + ".json"
	savedData, err := os.ReadFile(resumeFile) //nolint:gosec
	if err != nil {
		return false
	}

	var savedState tuiSavedState
	if err := json.Unmarshal(savedData, &savedState); err != nil {
		return false
	}

	m.setStatus(fmt.Sprintf("Resuming from %s (%d entries)", resumeFile, len(savedState.Entries)))

	if ts, err := time.Parse(time.RFC3339, savedState.StartTime); err == nil {
		m.startTime = ts
	}

	var staleCount int
	for _, entry := range savedState.Entries {
		var entryTime time.Time
		if entry.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
				entryTime = ts
			}
		}

		isStale := false
		if tuiMaxAge > 0 && entry.Phase == "done" && !entryTime.IsZero() {
			if time.Since(entryTime) > tuiMaxAge {
				isStale = true
				staleCount++
			}
		}

		if entry.Phase == "done" && !isStale {
			m.pingedTpIDs[entry.TpID] = true
		}

		// Check for duplicates before adding
		duplicate := false
		for _, e := range m.entries {
			if e.tpID == entry.TpID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			m.entries = append(m.entries, tuiTreeEntry{
				tpID:     entry.TpID,
				tpType:   entry.TpType,
				remotePK: entry.RemotePK,
				level:    entry.Level,
				parentPK: entry.ParentPK,
			})
		}

		var lastSuccessTime time.Time
		if entry.LastSuccess != "" {
			if ts, err := time.Parse(time.RFC3339, entry.LastSuccess); err == nil {
				lastSuccessTime = ts
			}
		}

		data := &tuiLatencyData{
			tpID:           entry.TpID,
			tpType:         entry.TpType,
			from:           entry.ParentPK,
			to:             entry.RemotePK,
			gatewayPK:      entry.GatewayPK,
			level:          entry.Level,
			calcTimeMs:     entry.CalcTimeMs,
			setupTimeMs:    entry.SetupTimeMs,
			pingSamples:    entry.PingSamples,
			calcErr:        entry.CalcErr,
			setupErr:       entry.SetupErr,
			pingErr:        entry.PingErr,
			phase:          entry.Phase,
			timestamp:      entryTime,
			lastSuccess:    lastSuccessTime,
			stale:          isStale,
			dmsgReachable:  entry.DmsgReachable,
			dmsgSkipReason: entry.DmsgSkipReason,
		}

		if isStale {
			data.phase = "pending"
		}

		m.latencies[entry.TpID] = data
	}

	return true
}

// removeLocalTransport removes a local transport
func (m *pingTreeModel) removeLocalTransport(tpID string) error {
	tpUUID, err := uuid.Parse(tpID)
	if err != nil {
		return fmt.Errorf("invalid transport ID: %w", err)
	}
	if err := m.rpcClient.RemoveTransport(tpUUID); err != nil {
		return fmt.Errorf("failed to remove local transport: %w", err)
	}
	delete(m.localTpIDs, tpID)
	return nil
}

// removeRemoteTransport requests remote visor to remove a transport via TPS
func (m *pingTreeModel) removeRemoteTransport(remotePK string, tpID string) error {
	var pk cipher.PubKey
	if err := pk.Set(remotePK); err != nil {
		return fmt.Errorf("invalid remote PK: %w", err)
	}
	tpUUID, err := uuid.Parse(tpID)
	if err != nil {
		return fmt.Errorf("invalid transport ID: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- m.rpcClient.TPSRemoveTransport(pk, tpUUID)
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to remove remote transport: %w", err)
		}
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timeout removing remote transport")
	}

	return nil
}

// loadDmsgClients loads DMSG clients data for pre-checking
func (m *pingTreeModel) loadDmsgClients() {
	if !tuiDmsgPreCheck && !tuiDmsgOnly {
		return
	}

	dmsgURL := tuiDMSGURL + "/dmsg-discovery/servers/clients"
	m.setStatus("Loading DMSG clients...")
	dmsgClientsRaw := internal.GetData(tuiCacheDMSG, dmsgURL, tuiCacheAge)

	var clientsByServer map[string][]string
	if err := json.Unmarshal([]byte(dmsgClientsRaw), &clientsByServer); err != nil {
		m.setStatus("Failed to load DMSG clients")
		return
	}

	newVisorServers := make(map[string][]string)
	for serverPK, clients := range clientsByServer {
		for _, clientPK := range clients {
			newVisorServers[clientPK] = append(newVisorServers[clientPK], serverPK)
		}
	}
	m.visorDmsgServers = newVisorServers
	m.dmsgClientsLoaded = true
	m.setStatus(fmt.Sprintf("Loaded DMSG data for %d clients", len(newVisorServers)))
}

// getVisorDmsgServers returns DMSG servers for a visor
func (m *pingTreeModel) getVisorDmsgServers(visorPK string) []string {
	if !m.dmsgClientsLoaded {
		return nil
	}
	return m.visorDmsgServers[visorPK]
}

// runPingTreeTUI is the main entry point
func runPingTreeTUI(cmd *cobra.Command, _ []string) {
	pterm.EnableStyling()

	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}

	grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to gRPC server: %w", err))
	}
	defer grpcClient.Close() //nolint:errcheck

	overview, err := rpcClient.Overview()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get visor overview: %w", err))
	}
	localPK := overview.PubKey.String()

	// Fetch TPD data
	tpdRaw := internal.GetData(tuiCacheTPD, tuiTPDURL+"/all-transports", tuiCacheAge)
	var transports []transportEntry
	if err := json.Unmarshal([]byte(tpdRaw), &transports); err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse TPD data: %w", err))
	}

	adjacency := make(map[string][]treeNeighbor)
	for _, tp := range transports {
		edge0, edge1 := tp.Edges[0], tp.Edges[1]
		adjacency[edge0] = append(adjacency[edge0], treeNeighbor{pk: edge1, tpID: tp.ID, tpType: tp.Type})
		if edge0 != edge1 {
			adjacency[edge1] = append(adjacency[edge1], treeNeighbor{pk: edge0, tpID: tp.ID, tpType: tp.Type})
		}
	}

	localTransports, err := rpcClient.Transports(nil, nil, false)
	if err != nil {
		fmt.Printf("Warning: failed to get local transports: %v\n", err)
	}

	localTpIDs := make(map[string]bool)
	localTpByRemote := make(map[string]treeNeighbor)
	for _, tp := range localTransports {
		tpID := tp.ID.String()
		remotePK := tp.Remote.String()
		tpType := string(tp.Type)

		localTpIDs[tpID] = true
		localTpByRemote[remotePK] = treeNeighbor{pk: remotePK, tpID: tpID, tpType: tpType}

		found := false
		for _, n := range adjacency[localPK] {
			if n.tpID == tpID {
				found = true
				break
			}
		}
		if !found {
			adjacency[localPK] = append(adjacency[localPK], treeNeighbor{pk: remotePK, tpID: tpID, tpType: tpType})
			adjacency[remotePK] = append(adjacency[remotePK], treeNeighbor{pk: localPK, tpID: tpID, tpType: tpType})
		}
	}

	// Fetch UT data
	utRaw := internal.GetData(tuiCacheUT, tuiUTURL+"/uptimes?v=v2", tuiCacheAge)
	var utEntries []uptimeEntry
	_ = json.Unmarshal([]byte(utRaw), &utEntries) //nolint:errcheck

	onlineSet := make(map[string]bool)
	versionFilteredSet := make(map[string]bool)
	filterByVersion := tuiVersion != ""

	var minVersion semver.Version
	if filterByVersion {
		cleanVersion := strings.TrimPrefix(tuiVersion, "v")
		minVersion, err = semver.Parse(cleanVersion)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid version format: %w", err))
		}
	}

	for _, entry := range utEntries {
		if entry.Online {
			onlineSet[entry.PK] = true
		}
		if filterByVersion && entry.Version != "" {
			cleanVer := strings.TrimPrefix(entry.Version, "v")
			cleanVer = strings.Fields(cleanVer)[0]
			cleanVer = strings.Split(cleanVer, "+")[0]
			cleanVer = strings.SplitN(cleanVer, "-", 2)[0]
			if v, err := semver.Parse(cleanVer); err == nil && v.GTE(minVersion) {
				versionFilteredSet[entry.PK] = true
			}
		}
	}

	passesFilter := func(pk string) bool {
		if tuiOnlineOnly && !onlineSet[pk] {
			return false
		}
		if filterByVersion && !versionFilteredSet[pk] {
			return false
		}
		return true
	}

	model := &pingTreeModel{
		ctx:              ctx,
		cancel:           cancel,
		grpcClient:       grpcClient,
		rpcClient:        rpcClient,
		localPK:          localPK,
		adjacency:        adjacency,
		localTps:         localTransports,
		passesFilter:     passesFilter,
		onlineSet:        onlineSet,
		entries:          []tuiTreeEntry{},
		entriesMu:        &sync.RWMutex{},
		latencies:        make(map[string]*tuiLatencyData),
		latenciesMu:      &sync.RWMutex{},
		pingedTpIDs:      make(map[string]bool),
		localTpIDs:       localTpIDs,
		localTpByRemote:  localTpByRemote,
		visorDmsgServers: make(map[string][]string),
		autoScroll:       true,
		startTime:        time.Now(),
		statusMu:         &sync.RWMutex{},
		pingWg:           &sync.WaitGroup{},
	}

	// Load DMSG clients if needed
	if tuiDmsgPreCheck || tuiDmsgOnly {
		model.loadDmsgClients()
	}

	// Load saved state if resuming
	model.loadSavedState()

	// Start the ping worker
	model.pingWg.Add(1)
	go func() {
		defer model.pingWg.Done()
		model.runPingWorker()
	}()

	// Auto-save periodically
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				model.saveResults()
			}
		}
	}()

	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// Cancel context and wait for all goroutines to finish before closing grpcClient
	cancel()
	model.pingWg.Wait()

	// Print final results to stdout so they remain visible after exit
	if m, ok := finalModel.(pingTreeModel); ok {
		fmt.Println("\n=== Final Results ===")
		fmt.Print(m.renderTreeContent())
		fmt.Printf("\nTotal: %d visors, %d pinged, %d failed\n", m.totalVisors, m.pingedVisors, m.failedVisors)
	}
}

// runPingWorker runs the ping tests in the background
func (m *pingTreeModel) runPingWorker() {
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		if tuiDmsgOnly {
			m.runDmsgMode()
		} else {
			m.runRouteMode()
		}

		if !tuiContinuous {
			m.setStatus("Done")
			return
		}

		m.setStatus("Waiting for recheck...")
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(tuiRecheckAge):
		}

		// Mark old entries as stale for re-ping
		m.markStaleEntries()
	}
}

// markStaleEntries marks entries older than recheckAge as stale
func (m *pingTreeModel) markStaleEntries() {
	m.latenciesMu.Lock()
	defer m.latenciesMu.Unlock()

	for _, data := range m.latencies {
		if data.phase == "done" && time.Since(data.timestamp) > tuiRecheckAge {
			data.stale = true
			data.phase = "pending"
			delete(m.pingedTpIDs, data.tpID)
		}
	}
}

// runRouteMode runs route-based ping tests
func (m *pingTreeModel) runRouteMode() {
	visited := make(map[string]bool)
	visited[m.localPK] = true

	// Process level 1 (direct transports)
	m.setStatus("Discovering level 1...")
	for _, tp := range m.localTps {
		remotePK := tp.Remote.String()
		if visited[remotePK] {
			continue
		}
		if !m.passesFilter(remotePK) {
			continue
		}
		if tuiHops > 0 && tuiHops != 1 {
			continue
		}

		entry := tuiTreeEntry{
			tpID:     tp.ID.String(),
			tpType:   string(tp.Type),
			remotePK: remotePK,
			level:    1,
			parentPK: m.localPK,
		}

		m.addEntry(entry)
		visited[remotePK] = true
	}

	// Ping level 1 entries
	m.pingLevel(1)

	// Expand to deeper levels
	if tuiMaxLevel == 0 || tuiMaxLevel > 1 {
		m.expandLevels(visited, 2)
	}
}

// runDmsgMode runs DMSG-based ping tests
func (m *pingTreeModel) runDmsgMode() {
	m.setStatus("Running DMSG-only mode...")

	// Get all online visors
	var targets []string
	for pk := range m.onlineSet {
		if pk == m.localPK {
			continue
		}
		if !m.passesFilter(pk) {
			continue
		}
		targets = append(targets, pk)
	}

	for i, remotePK := range targets {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		m.setStatus(fmt.Sprintf("DMSG ping %d/%d", i+1, len(targets)))

		entry := tuiTreeEntry{
			tpID:     fmt.Sprintf("dmsg-%s", remotePK[:16]),
			tpType:   "dmsg",
			remotePK: remotePK,
			level:    1,
			parentPK: m.localPK,
		}

		m.addEntry(entry)
		m.pingViaDmsg(entry)
	}
}

// addEntry adds an entry if not already present
func (m *pingTreeModel) addEntry(entry tuiTreeEntry) {
	m.entriesMu.Lock()
	defer m.entriesMu.Unlock()

	// Check if already exists
	for _, e := range m.entries {
		if e.tpID == entry.tpID {
			return
		}
	}

	m.entries = append(m.entries, entry)

	m.latenciesMu.Lock()
	if m.latencies[entry.tpID] == nil {
		m.latencies[entry.tpID] = &tuiLatencyData{
			tpID:   entry.tpID,
			tpType: entry.tpType,
			from:   entry.parentPK,
			to:     entry.remotePK,
			level:  entry.level,
			phase:  "pending",
		}
	}
	m.latenciesMu.Unlock()
}

// pingLevel pings all entries at a given level with concurrency limiting
// This function blocks until all pings at this level are complete.
func (m *pingTreeModel) pingLevel(level int) {
	m.entriesMu.RLock()
	var levelEntries []tuiTreeEntry
	for _, e := range m.entries {
		if e.level == level {
			levelEntries = append(levelEntries, e)
		}
	}
	m.entriesMu.RUnlock()

	// Use semaphore for concurrency limiting
	concurrency := tuiConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var completed int32

	// Local WaitGroup to ensure all pings at this level complete before returning
	// This is separate from m.pingWg which is used for cleanup on shutdown
	var levelWg sync.WaitGroup

	for _, entry := range levelEntries {
		select {
		case <-m.ctx.Done():
			// Wait for any already-started goroutines before returning
			levelWg.Wait()
			return
		default:
		}

		if m.pingedTpIDs[entry.tpID] {
			continue
		}

		if tuiDryRun {
			m.latenciesMu.Lock()
			if data := m.latencies[entry.tpID]; data != nil {
				data.phase = "done"
				data.timestamp = time.Now()
			}
			m.latenciesMu.Unlock()
			continue
		}

		// Acquire semaphore slot
		sem <- struct{}{}
		m.pingWg.Add(1)
		levelWg.Add(1)

		go func(entry tuiTreeEntry) {
			defer m.pingWg.Done()
			defer levelWg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					// Gracefully handle panic (e.g., gRPC client closed during shutdown)
					m.latenciesMu.Lock()
					if data := m.latencies[entry.tpID]; data != nil {
						data.pingErr = "interrupted"
						data.phase = "done"
					}
					m.latenciesMu.Unlock()
				}
			}()

			// Check context before starting work
			select {
			case <-m.ctx.Done():
				return
			default:
			}

			// Update status
			current := atomic.AddInt32(&completed, 1)
			m.setStatus(fmt.Sprintf("Level %d: %d/%d", level, current, len(levelEntries)))

			// DMSG pre-check if enabled
			if tuiDmsgPreCheck {
				if !m.checkDmsgReachable(entry) {
					return
				}
			}

			m.pingTransport(entry)
		}(entry)
	}

	// Wait for all pings at this level to complete before returning
	// This ensures levels are processed sequentially
	levelWg.Wait()
}

// expandLevels expands to deeper levels
func (m *pingTreeModel) expandLevels(visited map[string]bool, startLevel int) {
	currentLevel := startLevel
	for {
		if tuiMaxLevel > 0 && currentLevel > tuiMaxLevel {
			break
		}
		if tuiHops > 0 && currentLevel >= 0 && uint(currentLevel) != tuiHops { //nolint:gosec
			currentLevel++
			continue
		}

		var expandFrom []string
		m.entriesMu.RLock()
		for _, entry := range m.entries {
			if entry.level == currentLevel-1 && !entry.failed {
				expandFrom = append(expandFrom, entry.remotePK)
			}
		}
		m.entriesMu.RUnlock()

		if len(expandFrom) == 0 {
			break
		}

		m.setStatus(fmt.Sprintf("Discovering level %d...", currentLevel))
		newEntries := 0
		for _, parentPK := range expandFrom {
			select {
			case <-m.ctx.Done():
				return
			default:
			}

			neighbors := m.adjacency[parentPK]
			for _, neighbor := range neighbors {
				if visited[neighbor.pk] {
					continue
				}
				if !m.passesFilter(neighbor.pk) {
					continue
				}

				entry := tuiTreeEntry{
					tpID:     neighbor.tpID,
					tpType:   neighbor.tpType,
					remotePK: neighbor.pk,
					level:    currentLevel,
					parentPK: parentPK,
				}

				m.addEntry(entry)
				visited[neighbor.pk] = true
				newEntries++
			}
		}

		if newEntries == 0 {
			break
		}

		m.pingLevel(currentLevel)
		currentLevel++
	}
}

// checkDmsgReachable checks if a visor is reachable via DMSG
func (m *pingTreeModel) checkDmsgReachable(entry tuiTreeEntry) bool {
	servers := m.getVisorDmsgServers(entry.remotePK)
	if len(servers) == 0 {
		m.latenciesMu.Lock()
		if data := m.latencies[entry.tpID]; data != nil {
			data.dmsgSkipReason = "no DMSG servers"
		}
		m.latenciesMu.Unlock()
		return false
	}

	// Ping via each DMSG server and store results
	reachable := false
	for _, serverPK := range servers {
		select {
		case <-m.ctx.Done():
			return false
		default:
		}

		// Create server data entry
		serverData := &tuiDmsgServerData{
			serverPK: serverPK,
			phase:    "ping",
		}

		// Add to latency data immediately so it shows in tree
		m.latenciesMu.Lock()
		if data := m.latencies[entry.tpID]; data != nil {
			data.dmsgServers = append(data.dmsgServers, serverData)
		}
		m.latenciesMu.Unlock()

		// Perform DMSG ping via this server
		var samples []float64
		ctx, cancel := context.WithTimeout(m.ctx, tuiTimeout*time.Duration(tuiTries+1))
		err := m.grpcClient.StreamDmsgPing(ctx, entry.remotePK, int32(tuiTries), int32(tuiPcktSize), tuiTimeout, serverPK, //nolint:gosec
			func(_ int32, latency time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, _ time.Duration, pingErr error) {
				if isSetup {
					return
				}
				if pingErr != nil {
					serverData.pingErr = truncateError(pingErr.Error())
				} else {
					samples = append(samples, float64(latency.Milliseconds()))
				}
			})
		cancel()

		// Update server data with results
		serverData.pingSamples = samples
		serverData.phase = "done"
		serverData.timestamp = time.Now()

		if err != nil && serverData.pingErr == "" {
			serverData.pingErr = truncateError(err.Error())
		}

		// Mark reachable if we got any successful pings
		if len(samples) > 0 {
			reachable = true
			m.latenciesMu.Lock()
			if data := m.latencies[entry.tpID]; data != nil {
				data.dmsgReachable = true
			}
			m.latenciesMu.Unlock()
		}

		// Stop after first success unless --dmsg-all-servers
		if !tuiDmsgAllServers && reachable {
			break
		}
	}

	// Set skip reason if not reachable
	if !reachable {
		m.latenciesMu.Lock()
		if data := m.latencies[entry.tpID]; data != nil {
			data.dmsgSkipReason = "DMSG ping failed"
		}
		m.latenciesMu.Unlock()
	}

	return reachable
}

// pingViaDmsg pings a visor via DMSG servers
func (m *pingTreeModel) pingViaDmsg(entry tuiTreeEntry) {
	servers := m.getVisorDmsgServers(entry.remotePK)
	if len(servers) == 0 {
		m.latenciesMu.Lock()
		if data := m.latencies[entry.tpID]; data != nil {
			data.pingErr = "no DMSG servers"
			data.phase = "done"
			data.timestamp = time.Now()
		}
		m.latenciesMu.Unlock()
		m.markFailed(entry.tpID)
		return
	}

	m.latenciesMu.Lock()
	if data := m.latencies[entry.tpID]; data != nil {
		data.phase = "ping"
	}
	m.latenciesMu.Unlock()

	for _, serverPK := range servers {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		serverData := &tuiDmsgServerData{
			serverPK: serverPK,
			phase:    "ping",
		}

		var samples []float64
		ctx, cancel := context.WithTimeout(m.ctx, tuiTimeout*time.Duration(tuiTries+1))
		err := m.grpcClient.StreamDmsgPing(ctx, entry.remotePK, int32(tuiTries), int32(tuiPcktSize), tuiTimeout, serverPK, //nolint:gosec
			func(_ int32, latency time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, _ time.Duration, pingErr error) {
				if isSetup {
					return
				}
				if pingErr != nil {
					serverData.pingErr = truncateError(pingErr.Error())
				} else {
					samples = append(samples, float64(latency.Milliseconds()))
				}
			})
		cancel()

		serverData.pingSamples = samples
		serverData.phase = "done"
		serverData.timestamp = time.Now()

		if err != nil && serverData.pingErr == "" {
			serverData.pingErr = truncateError(err.Error())
		}

		m.latenciesMu.Lock()
		if data := m.latencies[entry.tpID]; data != nil {
			data.dmsgServers = append(data.dmsgServers, serverData)
			if len(samples) > 0 {
				data.dmsgReachable = true
				// Use best DMSG result for main samples
				if len(data.pingSamples) == 0 || samples[0] < data.pingSamples[0] {
					data.pingSamples = samples
				}
			}
		}
		m.latenciesMu.Unlock()

		if !tuiDmsgAllServers && len(samples) > 0 {
			break
		}
	}

	m.latenciesMu.Lock()
	if data := m.latencies[entry.tpID]; data != nil {
		data.phase = "done"
		data.timestamp = time.Now()
		if len(data.pingSamples) == 0 {
			m.latenciesMu.Unlock()
			m.markFailed(entry.tpID)
			return
		}
	}
	m.latenciesMu.Unlock()
	m.pingedTpIDs[entry.tpID] = true
}

// pingTransport pings a single transport via route
func (m *pingTreeModel) pingTransport(entry tuiTreeEntry) {
	m.latenciesMu.Lock()
	data := m.latencies[entry.tpID]
	if data == nil {
		data = &tuiLatencyData{
			tpID:   entry.tpID,
			tpType: entry.tpType,
			from:   entry.parentPK,
			to:     entry.remotePK,
			level:  entry.level,
			phase:  "pending",
		}
		m.latencies[entry.tpID] = data
	}
	data.phase = "setup"
	m.latenciesMu.Unlock()

	var samples []float64
	var setupTimeMs float64
	var calcTimeMs float64
	var setupErr, pingErr, calcErr string

	callback := func(_ int32, latency time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, routeCalcTime time.Duration, err error) {
		if isSetup {
			setupTimeMs = float64(latency.Milliseconds())
			calcTimeMs = float64(routeCalcTime.Milliseconds())
			if err != nil {
				setupErr = truncateError(err.Error())
			}
			m.latenciesMu.Lock()
			data.setupTimeMs = setupTimeMs
			data.calcTimeMs = calcTimeMs
			if setupErr != "" {
				data.setupErr = setupErr
			}
			data.phase = "ping"
			m.latenciesMu.Unlock()
		} else {
			if err != nil {
				pingErr = truncateError(err.Error())
			} else {
				samples = append(samples, float64(latency.Milliseconds()))
				m.latenciesMu.Lock()
				data.pingSamples = samples
				m.latenciesMu.Unlock()
			}
		}
	}

	ctx, cancel := context.WithTimeout(m.ctx, tuiSetupTimeout+tuiTimeout*time.Duration(tuiTries+1))
	err := m.grpcClient.StreamPingWithTransport(
		ctx,
		entry.remotePK,
		int32(tuiTries),    //nolint:gosec
		int32(tuiPcktSize), //nolint:gosec
		true,
		tuiTimeout,
		tuiSetupTimeout,
		entry.tpID,
		callback,
	)
	cancel()

	// Handle retries
	retryCount := 0
	for (setupErr != "" || pingErr != "" || len(samples) == 0) && retryCount < tuiRetries {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		retryCount++
		samples = nil
		setupErr = ""
		pingErr = ""

		m.latenciesMu.Lock()
		data.phase = "setup"
		data.setupErr = ""
		data.pingErr = ""
		data.pingSamples = nil
		m.latenciesMu.Unlock()

		ctx, cancel := context.WithTimeout(m.ctx, tuiSetupTimeout+tuiTimeout*time.Duration(tuiTries+1))
		err = m.grpcClient.StreamPingWithTransport(ctx, entry.remotePK, int32(tuiTries), int32(tuiPcktSize), true, tuiTimeout, tuiSetupTimeout, entry.tpID, callback) //nolint:gosec
		cancel()
	}

	// Finalize
	m.latenciesMu.Lock()
	if err != nil && data.setupErr == "" && data.pingErr == "" {
		data.pingErr = truncateError(err.Error())
	}
	if calcErr != "" && data.calcErr == "" {
		data.calcErr = calcErr
	}
	if pingErr != "" && data.pingErr == "" {
		data.pingErr = pingErr
	}
	data.phase = "done"
	data.timestamp = time.Now()

	failed := data.setupErr != "" || data.pingErr != "" || data.calcErr != "" || len(data.pingSamples) == 0
	if !failed {
		data.lastSuccess = time.Now()
	}
	m.latenciesMu.Unlock()

	if failed {
		m.handleFailedTransport(entry)
	} else {
		m.pingedTpIDs[entry.tpID] = true
	}
}

// handleFailedTransport handles a failed transport (removal/remake)
func (m *pingTreeModel) handleFailedTransport(entry tuiTreeEntry) {
	m.markFailed(entry.tpID)

	isLevel1 := entry.level == 1

	// Remove local transport if requested
	if tuiRemoveTp && isLevel1 && m.localTpIDs[entry.tpID] {
		if err := m.removeLocalTransport(entry.tpID); err != nil {
			m.entriesMu.Lock()
			for i := range m.entries {
				if m.entries[i].tpID == entry.tpID {
					m.entries[i].removeErr = err.Error()
					break
				}
			}
			m.entriesMu.Unlock()
		} else {
			m.entriesMu.Lock()
			for i := range m.entries {
				if m.entries[i].tpID == entry.tpID {
					m.entries[i].removed = true
					break
				}
			}
			m.entriesMu.Unlock()
		}
	}

	// Remove remote transport if requested
	if tuiRemoveRemoteTp && isLevel1 {
		_ = m.removeRemoteTransport(entry.remotePK, entry.tpID) //nolint:errcheck
	}

	// Remake transport if requested and not already remade
	m.entriesMu.RLock()
	var alreadyRemade bool
	for _, e := range m.entries {
		if e.tpID == entry.tpID {
			alreadyRemade = e.remadeOnce
			break
		}
	}
	m.entriesMu.RUnlock()

	if (tuiRemakeTp || tuiRemakeRemoteTp) && isLevel1 && !alreadyRemade {
		m.entriesMu.Lock()
		for i := range m.entries {
			if m.entries[i].tpID == entry.tpID {
				m.entries[i].remadeOnce = true
				break
			}
		}
		m.entriesMu.Unlock()

		// Try to recreate via TPS
		var remotePKObj, localPKObj cipher.PubKey
		if err := remotePKObj.Set(entry.remotePK); err == nil {
			if err := localPKObj.Set(m.localPK); err == nil {
				newTps, err := m.rpcClient.TPSAddTransport(localPKObj, remotePKObj, entry.tpType)
				if err == nil && newTps != nil {
					m.entriesMu.Lock()
					for i := range m.entries {
						if m.entries[i].tpID == entry.tpID {
							m.entries[i].remade = true
							break
						}
					}
					m.entriesMu.Unlock()
				}
			}
		}
	}

	m.pingedTpIDs[entry.tpID] = true
}

// markFailed marks an entry as failed
func (m *pingTreeModel) markFailed(tpID string) {
	m.entriesMu.Lock()
	defer m.entriesMu.Unlock()
	for i := range m.entries {
		if m.entries[i].tpID == tpID {
			m.entries[i].failed = true
			break
		}
	}
}

// truncateError truncates an error message to 20 characters
func truncateError(s string) string {
	const maxLen = 20
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
