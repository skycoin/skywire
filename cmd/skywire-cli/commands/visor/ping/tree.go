// Package ping cmd/skywire-cli/commands/visor/ping/tree.go: the
// interactive Bubble Tea TUI for `cli visor ping tree`.
//
// History: this command used to host a ~2300-line client-side BFS
// + concurrency-limited ping orchestrator + state machine. That code
// had several structural problems on visors with hundreds of
// transports: default concurrency=2 capped throughput at ~4 pings/
// minute (so level 1 of a 500-transport visor took >2 hours and
// level 2 never started), the renderer hid pending entries (making
// progress invisible), and the Bubble Tea TUI's /dev/tty
// requirement made the tool undriveable from CI or coding agents.
//
// #2732 moved the BFS server-side as the StreamPingTree gRPC RPC.
// This file now consumes that stream and renders it with the same
// Bubble Tea TUI shape — header, stats line, scrollable tree
// viewport, footer — that operators were already familiar with.
//
// The NDJSON-driven sibling lives in tree_stream.go (`cli visor ping
// tree-stream`); it feeds the same events to stdout for treeprobe +
// CI consumers. Both subcommands ride the same server-side BFS
// implementation, so a fix at the server lands in both.
package ping

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// ---------------------------------------------------------------------------
// Flags
// ---------------------------------------------------------------------------

// pingTreeFlags is the subset of PingTreeRequest fields that operators
// commonly override. Server-side defaults (in normalizePingTreeRequest)
// kick in when a flag is zero. Flags that controlled client-side BFS
// state machinery in the old implementation (--concurrency, --continuous,
// --max-age, --remove-tp, --remake-tp, etc.) are dropped: concurrency is
// a server-side knob now, continuous-rerun is dead weight for a one-
// shot measurement tool, and transport mutations belong in cli tp.
type pingTreeFlags struct {
	MaxLevel     int
	Hops         int
	Tries        int
	Size         int
	Concurrency  int
	Timeout      time.Duration
	SetupTimeout time.Duration
	OnlineOnly   bool
	Version      string
	UseTpLat     bool
	DmsgOnly     bool
	DmsgPreCheck bool
	Retries      int
	DryRun       bool
	OutputFile   string
}

var treeFlags pingTreeFlags

func init() {
	pingTreeCmd.Flags().IntVarP(&treeFlags.MaxLevel, "max-level", "l", 0,
		"maximum BFS depth (0 = unlimited until expansion exhausts)")
	pingTreeCmd.Flags().IntVar(&treeFlags.Hops, "hops", 0,
		"ping ONLY entries at exactly N hops; other levels are discovered but not pinged")
	pingTreeCmd.Flags().IntVarP(&treeFlags.Tries, "tries", "t", 1,
		"per-transport ping count; PingResult carries aggregated stats")
	pingTreeCmd.Flags().IntVarP(&treeFlags.Size, "size", "s", 2,
		"packet size in KB")
	pingTreeCmd.Flags().IntVarP(&treeFlags.Concurrency, "concurrency", "c", 0,
		"max in-flight pings per BFS level (0 = server default, currently 16)")
	pingTreeCmd.Flags().DurationVarP(&treeFlags.Timeout, "timeout", "o", 30*time.Second,
		"per-ping timeout (after route setup)")
	pingTreeCmd.Flags().DurationVar(&treeFlags.SetupTimeout, "setup-timeout", 30*time.Second,
		"per-transport route-setup timeout")
	pingTreeCmd.Flags().BoolVarP(&treeFlags.OnlineOnly, "online", "g", false,
		"only ping visors marked online in the uptime tracker")
	pingTreeCmd.Flags().StringVarP(&treeFlags.Version, "version", "v", "",
		"filter by minimum visor version (semver)")
	pingTreeCmd.Flags().BoolVar(&treeFlags.UseTpLat, "use-transport-latency", true,
		"at level 1: skip the live ping when the transport already has a smoothed RTT in TransportSummary.LatencyMS")
	pingTreeCmd.Flags().BoolVar(&treeFlags.DmsgOnly, "dmsg-only", false,
		"force the ping path to ride DMSG instead of the skywire router")
	pingTreeCmd.Flags().BoolVar(&treeFlags.DmsgPreCheck, "dmsg-precheck", false,
		"probe DMSG reachability before each route ping; discards unreachable visors early")
	pingTreeCmd.Flags().IntVar(&treeFlags.Retries, "retries", 0,
		"retry attempts on failed pings")
	pingTreeCmd.Flags().BoolVar(&treeFlags.DryRun, "dry-run", false,
		"discovery only; no PingResult events fire (every entry marked latency_source=skipped)")
	pingTreeCmd.Flags().StringVarP(&treeFlags.OutputFile, "output", "O", "",
		"append per-event NDJSON to FILE as the run progresses (for offline analysis)")

	RootCmd.AddCommand(pingTreeCmd)
}

// ---------------------------------------------------------------------------
// Cobra command
// ---------------------------------------------------------------------------

var pingTreeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Interactive Bubble Tea TUI for the ping-tree (server-side BFS over the skywire route graph)",
	Long: `Walk the visor's neighborhood breadth-first, pinging each
discovered visor and rendering the results as a scrollable tree.

The BFS runs server-side via the StreamPingTree gRPC RPC (see
#2732 / pkg/visor/rpcgrpc/server_ping_tree.go); this command is a
thin Bubble Tea TUI on top of that stream.

The non-interactive sibling 'cli visor ping tree-stream' emits the
same events as NDJSON on stdout — use that one for CI, coding-agent
automation, or piping into the treeprobe harness (pkg/util/treeprobe).

Examples:

  # Walk all reachable levels:
  skywire cli visor ping tree

  # Only level 1 (direct neighbors), 5 ping samples each:
  skywire cli visor ping tree --max-level 1 --tries 5

  # Specific hop count for latency-by-hops measurement:
  skywire cli visor ping tree --hops 2 --max-level 2 --tries 5

  # Discovery-only, no pings (visualize the reachable graph):
  skywire cli visor ping tree --dry-run --max-level 2

Controls inside the TUI:
  ↑/k, ↓/j     scroll one line
  PgUp/PgDn    page up/down
  Home/End     top/bottom
  a            toggle auto-scroll
  q/Ctrl+C     quit`,
	Run: runPingTree,
}

// runPingTree opens the StreamPingTree RPC, starts a Bubble Tea
// program that consumes events, and blocks until the TUI exits.
// Ctrl+C or 'q' inside the TUI cancels the upstream context, which
// tears down the BFS within one in-flight ping.
func runPingTree(cmd *cobra.Command, _ []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ping tree: gRPC client connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close() //nolint:errcheck

	req := buildPingTreeRequest()
	stream, err := client.StreamPingTree(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ping tree: StreamPingTree call: %v\n", err)
		os.Exit(1)
	}

	model := newPingTreeModel(ctx, cancel)

	// Optional NDJSON tee-to-file. When set, we open the file once
	// up-front and the stream consumer goroutine writes one line per
	// event. The TUI runs in parallel — operator gets visual + file
	// in one shot.
	if treeFlags.OutputFile != "" {
		f, openErr := os.OpenFile(treeFlags.OutputFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "ping tree: open --output file: %v\n", openErr)
			os.Exit(1)
		}
		defer f.Close() //nolint:errcheck
		model.outputFile = f
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	go consumeStream(stream, p, model)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ping tree: TUI: %v\n", err)
		os.Exit(1)
	}
}

func buildPingTreeRequest() *rpcgrpc.PingTreeRequest {
	return &rpcgrpc.PingTreeRequest{
		MaxLevel:            int32(treeFlags.MaxLevel), //nolint:gosec
		Hops:                int32(treeFlags.Hops),     //nolint:gosec
		Tries:               int32(treeFlags.Tries),    //nolint:gosec
		PacketSizeKb:        int32(treeFlags.Size),     //nolint:gosec
		PingTimeoutNs:       treeFlags.Timeout.Nanoseconds(),
		SetupTimeoutNs:      treeFlags.SetupTimeout.Nanoseconds(),
		Concurrency:         int32(treeFlags.Concurrency), //nolint:gosec
		OnlineOnly:          treeFlags.OnlineOnly,
		MinVersion:          treeFlags.Version,
		UseTransportLatency: treeFlags.UseTpLat,
		DmsgOnly:            treeFlags.DmsgOnly,
		DmsgPreCheck:        treeFlags.DmsgPreCheck,
		Retries:             int32(treeFlags.Retries), //nolint:gosec
		DryRun:              treeFlags.DryRun,
	}
}

// ---------------------------------------------------------------------------
// Stream consumer
// ---------------------------------------------------------------------------

// consumeStream reads events off the gRPC stream and forwards each
// one to the Bubble Tea program via p.Send(eventMsg{...}). When the
// stream closes (RunDone or EOF), it sends a streamDoneMsg so the
// TUI can switch to "press q to exit" mode without quitting outright
// — operators may want to scroll through results.
func consumeStream(stream rpcgrpc.PingService_StreamPingTreeClient, p *tea.Program, m *pingTreeModel) {
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				p.Send(streamDoneMsg{})
				return
			}
			p.Send(streamErrMsg{err: err})
			return
		}
		if m.outputFile != nil {
			writeNDJSONLine(m.outputFile, ev)
		}
		p.Send(eventMsg{ev: ev})
	}
}

// writeNDJSONLine emits one NDJSON line per event when --output is
// set. Mirrors the envelope shape tree-stream uses so consumers can
// share parsers (treeprobe accepts either source).
func writeNDJSONLine(f *os.File, ev *rpcgrpc.PingTreeEvent) {
	typ, _ := classifyEvent(ev)
	envelope := map[string]any{
		"ts":   time.Unix(0, ev.TimestampNs).UTC().Format(time.RFC3339Nano),
		"type": typ,
	}
	// Reuse the proto JSON wire shape via encoding/json's
	// general-purpose encoder — fine for the file tee path because
	// the consumer is treeprobe, which already handles both
	// protojson-style and stdlib-style int64 encodings.
	switch p := ev.Payload.(type) {
	case *rpcgrpc.PingTreeEvent_Discovered:
		envelope["data"] = p.Discovered
	case *rpcgrpc.PingTreeEvent_PingResult:
		envelope["data"] = p.PingResult
	case *rpcgrpc.PingTreeEvent_LevelDone:
		envelope["data"] = p.LevelDone
	case *rpcgrpc.PingTreeEvent_RunDone:
		envelope["data"] = p.RunDone
	case *rpcgrpc.PingTreeEvent_StatusUpdate:
		envelope["data"] = p.StatusUpdate
	case *rpcgrpc.PingTreeEvent_ServerError:
		envelope["data"] = p.ServerError
	}
	b, _ := json.Marshal(envelope) //nolint:errcheck
	_, _ = f.Write(b)              //nolint:errcheck
	_, _ = f.Write([]byte("\n"))   //nolint:errcheck
}

func classifyEvent(ev *rpcgrpc.PingTreeEvent) (string, any) {
	switch p := ev.Payload.(type) {
	case *rpcgrpc.PingTreeEvent_Discovered:
		return "discovered", p.Discovered
	case *rpcgrpc.PingTreeEvent_PingResult:
		return "ping_result", p.PingResult
	case *rpcgrpc.PingTreeEvent_LevelDone:
		return "level_done", p.LevelDone
	case *rpcgrpc.PingTreeEvent_RunDone:
		return "run_done", p.RunDone
	case *rpcgrpc.PingTreeEvent_StatusUpdate:
		return "status_update", p.StatusUpdate
	case *rpcgrpc.PingTreeEvent_ServerError:
		return "server_error", p.ServerError
	}
	return "unknown", nil
}

// ---------------------------------------------------------------------------
// Bubble Tea model
// ---------------------------------------------------------------------------

// treeEntry is one (transport, peer) pair as the TUI sees it. The
// model maintains one per tp_id keyed by Discovered events; ping
// results update the same struct in place so the renderer can show
// "pending" → "success/fail" transitions without losing the
// discovery order.
type treeEntry struct {
	tpID, tpType   string
	remotePK       string
	parentPK       string
	level          int
	pinged         bool
	failed         bool
	canceled       bool
	latencySource  string // "live_ping" | "transport_summary" | "skipped"
	setupLatencyMs float64
	pingAvgMs      float64
	pingP50Ms      float64
	pingP99Ms      float64
	jitterMs       float64
	sampleCount    int32
	setupErr       string
	pingErr        string
	calcErr        string
	ts             time.Time
}

// levelInfo carries the LevelDone summary for header rendering.
type levelInfo struct {
	attempted     int32
	succeeded     int32
	failed        int32
	skippedCached int32
	done          bool
}

// runSummary mirrors PingTreeRunDone for final-section rendering.
type runSummary struct {
	totalDiscovered    int32
	totalPinged        int32
	totalSucceeded     int32
	totalFailed        int32
	totalSkippedCached int32
	wallMs             int64
	peakInFlight       int32
	terminationReason  string
}

type pingTreeModel struct {
	viewport viewport.Model
	spinner  spinner.Model

	ready      bool
	quitting   bool
	width      int
	height     int
	autoScroll bool

	mu          sync.RWMutex
	entries     map[string]*treeEntry // tp_id → entry
	entryOrder  []string              // tp_id insert order; ties to discovery order at each level
	levels      map[int32]*levelInfo  // level → info
	statusPhase string
	statusInFly int32
	statusPend  int32
	statusText  string
	runDone     *runSummary
	serverError string
	streamErr   error
	streamEnded bool

	ctx      context.Context
	cancel   context.CancelFunc
	runStart time.Time

	// outputFile is the optional --output NDJSON tee. Owned by the
	// command; nil when not set.
	outputFile *os.File
}

func newPingTreeModel(ctx context.Context, cancel context.CancelFunc) *pingTreeModel {
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))

	return &pingTreeModel{
		spinner:    sp,
		entries:    make(map[string]*treeEntry),
		levels:     make(map[int32]*levelInfo),
		autoScroll: true,
		ctx:        ctx,
		cancel:     cancel,
		runStart:   time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Bubble Tea Update / Init / View
// ---------------------------------------------------------------------------

// eventMsg wraps one PingTreeEvent from the gRPC stream. The
// stream-consumer goroutine sends one per event via p.Send.
type eventMsg struct{ ev *rpcgrpc.PingTreeEvent }

// streamDoneMsg fires when the gRPC stream closes cleanly (EOF
// after a RunDone). The TUI stops the spinner and shows
// "press q to exit" in the footer.
type streamDoneMsg struct{}

// streamErrMsg fires when stream.Recv() returns an error other
// than EOF. The TUI surfaces the error inline.
type streamErrMsg struct{ err error }

// tickMsg drives the elapsed-time counter and re-renders the tree
// even when no new events have arrived (so the operator sees the
// spinner moving and the elapsed timer ticking).
type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *pingTreeModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickCmd())
}

func (m *pingTreeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch v := msg.(type) {
	case tea.KeyMsg:
		switch v.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		case "a":
			m.autoScroll = !m.autoScroll
		case "up", "k":
			m.viewport.ScrollUp(1)
			m.autoScroll = false
		case "down", "j":
			m.viewport.ScrollDown(1)
			m.autoScroll = false
		case "pgup":
			m.viewport.HalfPageUp()
			m.autoScroll = false
		case "pgdown":
			m.viewport.HalfPageDown()
			m.autoScroll = false
		case "home", "g":
			m.viewport.GotoTop()
			m.autoScroll = false
		case "end", "G":
			m.viewport.GotoBottom()
			m.autoScroll = true
		}

	case tea.WindowSizeMsg:
		if !m.ready {
			m.viewport = viewport.New(v.Width, v.Height-3)
			m.viewport.SetContent(m.renderTree())
			m.ready = true
		} else {
			m.viewport.Width = v.Width
			m.viewport.Height = v.Height - 3
		}
		m.width, m.height = v.Width, v.Height

	case spinner.TickMsg:
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		cmds = append(cmds, spinnerCmd)

	case tickMsg:
		// Re-render to refresh elapsed-time counter + status line.
		m.viewport.SetContent(m.renderTree())
		if m.autoScroll {
			m.viewport.GotoBottom()
		}
		if !m.streamEnded {
			cmds = append(cmds, tickCmd())
		}

	case eventMsg:
		m.applyEvent(v.ev)
		m.viewport.SetContent(m.renderTree())
		if m.autoScroll {
			m.viewport.GotoBottom()
		}

	case streamDoneMsg:
		m.streamEnded = true
		m.viewport.SetContent(m.renderTree())

	case streamErrMsg:
		m.streamErr = v.err
		m.streamEnded = true
		m.viewport.SetContent(m.renderTree())
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)
	return m, tea.Batch(cmds...)
}

// applyEvent folds one PingTreeEvent into the model state. Called
// from the Update message handler; takes the model's write lock so
// the renderer (which holds the read lock) doesn't see partial
// updates.
func (m *pingTreeModel) applyEvent(ev *rpcgrpc.PingTreeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch p := ev.Payload.(type) {
	case *rpcgrpc.PingTreeEvent_Discovered:
		d := p.Discovered
		if _, exists := m.entries[d.TpId]; !exists {
			m.entries[d.TpId] = &treeEntry{
				tpID:     d.TpId,
				tpType:   d.TpType,
				remotePK: d.RemotePk,
				parentPK: d.ParentPk,
				level:    int(d.Level),
				ts:       time.Unix(0, ev.TimestampNs),
			}
			m.entryOrder = append(m.entryOrder, d.TpId)
		}

	case *rpcgrpc.PingTreeEvent_PingResult:
		r := p.PingResult
		e, ok := m.entries[r.TpId]
		if !ok {
			// PingResult arrived without a prior Discovered.
			// Shouldn't happen for the server-side BFS, but be
			// defensive: synthesize an entry so the renderer
			// still surfaces the result.
			e = &treeEntry{
				tpID:     r.TpId,
				tpType:   r.TpType,
				remotePK: r.RemotePk,
				parentPK: r.ParentPk,
				level:    int(r.Level),
				ts:       time.Unix(0, ev.TimestampNs),
			}
			m.entries[r.TpId] = e
			m.entryOrder = append(m.entryOrder, r.TpId)
		}
		e.pinged = true
		e.failed = r.Failed
		e.canceled = r.Canceled
		e.latencySource = r.LatencySource
		e.setupLatencyMs = float64(r.SetupLatencyNs) / 1e6
		e.pingAvgMs = float64(r.PingAvgNs) / 1e6
		e.pingP50Ms = float64(r.PingP50Ns) / 1e6
		e.pingP99Ms = float64(r.PingP99Ns) / 1e6
		e.jitterMs = float64(r.JitterNs) / 1e6
		e.sampleCount = r.SampleCount
		e.setupErr = r.SetupErr
		e.pingErr = r.PingErr
		e.calcErr = r.CalcErr

	case *rpcgrpc.PingTreeEvent_LevelDone:
		l := p.LevelDone
		m.levels[l.Level] = &levelInfo{
			attempted:     l.Attempted,
			succeeded:     l.Succeeded,
			failed:        l.Failed,
			skippedCached: l.SkippedCached,
			done:          true,
		}

	case *rpcgrpc.PingTreeEvent_RunDone:
		r := p.RunDone
		m.runDone = &runSummary{
			totalDiscovered:    r.TotalDiscovered,
			totalPinged:        r.TotalPinged,
			totalSucceeded:     r.TotalSucceeded,
			totalFailed:        r.TotalFailed,
			totalSkippedCached: r.TotalSkippedCached,
			wallMs:             r.WallTimeNs / 1e6,
			peakInFlight:       r.PeakInFlight,
			terminationReason:  r.TerminationReason,
		}

	case *rpcgrpc.PingTreeEvent_StatusUpdate:
		s := p.StatusUpdate
		m.statusPhase = s.Phase
		m.statusInFly = s.InFlight
		m.statusPend = s.Pending
		m.statusText = s.Message

	case *rpcgrpc.PingTreeEvent_ServerError:
		e := p.ServerError
		m.serverError = fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
}

func (m *pingTreeModel) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}
	if !m.ready {
		return "Initializing...\n"
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	header := headerStyle.Render("Ping Tree (gRPC streaming, server-side BFS)")

	stats := m.statsLine()

	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	scrollPct := m.viewport.ScrollPercent() * 100
	scrollIndicator := fmt.Sprintf("%.0f%%", scrollPct)
	if m.autoScroll {
		scrollIndicator += " [auto]"
	}
	hint := "↑/↓ scroll | PgUp/PgDn page | a toggle auto-scroll | q quit"
	if m.streamEnded {
		hint = "Run complete — q to exit"
	}
	footer := footerStyle.Render(fmt.Sprintf("%s | %s", hint, scrollIndicator))

	return fmt.Sprintf("%s\n%s\n%s\n%s", header, stats, m.viewport.View(), footer)
}

// statsLine renders the per-frame summary above the viewport. Pulls
// from m's locked state. Format mirrors the pre-rewire output:
// "Visors: A/B pinged, F failed | Elapsed: T | [status]".
func (m *pingTreeModel) statsLine() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var pinged, failed, discovered int
	for _, e := range m.entries {
		if e.pinged {
			pinged++
		}
		if e.failed {
			failed++
		}
		discovered++
	}
	elapsed := time.Since(m.runStart).Truncate(time.Second)
	stats := fmt.Sprintf("Visors: %d/%d pinged, %d failed | Elapsed: %s",
		pinged, discovered, failed, elapsed)
	if m.statusPhase != "" && !m.streamEnded {
		stats += fmt.Sprintf(" | %s %s (inflight=%d pending=%d)",
			m.spinner.View(), m.statusPhase, m.statusInFly, m.statusPend)
	}
	if m.serverError != "" {
		stats += " | server error: " + m.serverError
	}
	if m.streamErr != nil {
		stats += " | stream error: " + m.streamErr.Error()
	}
	return stats
}

// renderTree builds the scrollable viewport content. Sections per
// level, sorted by latency within each level (succeeded ascending,
// then pending, then failed). Final RunDone summary at the bottom.
func (m *pingTreeModel) renderTree() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Group entries by level.
	byLevel := make(map[int][]*treeEntry)
	for _, tpID := range m.entryOrder {
		e := m.entries[tpID]
		byLevel[e.level] = append(byLevel[e.level], e)
	}

	// Sort levels ascending so the renderer walks 1, 2, 3, ...
	levelKeys := make([]int, 0, len(byLevel))
	for k := range byLevel {
		levelKeys = append(levelKeys, k)
	}
	sort.Ints(levelKeys)

	out := ""
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	cacheStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	liveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	pendStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	for _, lv := range levelKeys {
		entries := byLevel[lv]
		levelHeader := fmt.Sprintf("=== Level %d (%d entries", lv, len(entries))
		if info, ok := m.levels[int32(lv)]; ok && info.done { //nolint:gosec
			levelHeader += fmt.Sprintf(" — cached:%d live:%d failed:%d", info.skippedCached, info.succeeded-info.skippedCached, info.failed)
		}
		levelHeader += ") ==="
		out += headStyle.Render(levelHeader) + "\n"

		// Sort: succeeded by ascending avg ms, then pending, then failed.
		sort.SliceStable(entries, func(i, j int) bool {
			ai, aj := entries[i], entries[j]
			cati := entryCategory(ai)
			catj := entryCategory(aj)
			if cati != catj {
				return cati < catj
			}
			if cati == 0 { // succeeded — sort by avg ms
				return ai.pingAvgMs < aj.pingAvgMs
			}
			// pending / failed — sort by discovery time
			return ai.ts.Before(aj.ts)
		})

		for _, e := range entries {
			line := formatEntryLine(e)
			switch entryCategory(e) {
			case 0:
				if e.latencySource == "transport_summary" {
					line = cacheStyle.Render(line)
				} else {
					line = liveStyle.Render(line)
				}
			case 1:
				line = pendStyle.Render(line)
			case 2:
				line = failStyle.Render(line)
			}
			out += line + "\n"
		}
		out += "\n"
	}

	if m.runDone != nil {
		out += headStyle.Render("=== Run Summary ===") + "\n"
		out += fmt.Sprintf(
			"discovered=%d pinged=%d succeeded=%d failed=%d skipped_cached=%d\n"+
				"wall_time=%dms peak_in_flight=%d termination=%s\n",
			m.runDone.totalDiscovered, m.runDone.totalPinged,
			m.runDone.totalSucceeded, m.runDone.totalFailed,
			m.runDone.totalSkippedCached, m.runDone.wallMs,
			m.runDone.peakInFlight, m.runDone.terminationReason,
		)
	}
	return out
}

// entryCategory returns 0 for successful pings, 1 for pending
// (discovered but not yet pinged), 2 for failed. The renderer sorts
// by this category so successes float to the top of each level.
func entryCategory(e *treeEntry) int {
	if !e.pinged {
		return 1
	}
	if e.failed {
		return 2
	}
	return 0
}

func formatEntryLine(e *treeEntry) string {
	switch entryCategory(e) {
	case 1: // pending
		return fmt.Sprintf("  · %s  %s  (pending)", e.remotePK[:12], e.tpType)
	case 2: // failed
		errMsg := e.setupErr
		if errMsg == "" {
			errMsg = e.pingErr
		}
		if errMsg == "" {
			errMsg = e.calcErr
		}
		if len(errMsg) > 60 {
			errMsg = errMsg[:60] + "..."
		}
		prefix := "✗"
		if e.canceled {
			prefix = "⊘" // canceled glyph
		}
		return fmt.Sprintf("  %s %s  %s  %s", prefix, e.remotePK[:12], e.tpType, errMsg)
	}
	// succeeded — distinguish cache vs live in the prefix
	srcTag := "live"
	if e.latencySource == "transport_summary" {
		srcTag = "cache"
	}
	if e.sampleCount > 1 {
		return fmt.Sprintf(
			"  ✓ %s  %-6s [%s] avg=%6.1fms p50=%6.1fms p99=%6.1fms jit=%5.1fms n=%d setup=%5.1fms",
			e.remotePK[:12], e.tpType, srcTag,
			e.pingAvgMs, e.pingP50Ms, e.pingP99Ms, e.jitterMs, e.sampleCount, e.setupLatencyMs,
		)
	}
	return fmt.Sprintf(
		"  ✓ %s  %-6s [%s] avg=%6.1fms setup=%5.1fms n=%d",
		e.remotePK[:12], e.tpType, srcTag,
		e.pingAvgMs, e.setupLatencyMs, e.sampleCount,
	)
}
