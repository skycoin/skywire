// Package clivisorping cmd/skywire-cli/commands/visor/ping/tree_tui.go c5-cli-visor
//
// The screen of `visor ping tree`; the command spec stays in tree.go.
package ping

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// runPingTree opens the StreamPingTree RPC and draws its events until the
// user quits, which cancels the BFS within one in-flight ping.
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

	model := newPingTreeModel()

	// Optional NDJSON tee-to-file, written by the stream consumer while
	// the screen runs.
	if treeFlags.OutputFile != "" {
		f, openErr := os.OpenFile(treeFlags.OutputFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "ping tree: open --output file: %v\n", openErr)
			os.Exit(1)
		}
		defer f.Close() //nolint:errcheck
		model.outputFile = f
	}

	err = runStreamView(streamView{
		top: func(spin string) string {
			return sgr(205, true)("Ping Tree (gRPC streaming, server-side BFS)") + "\n" + model.statsLine(spin)
		},
		body: model.renderTree,
		hint: model.hint,
		feed: func(changed func()) { go consumeStream(stream, model, changed) },
	})
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ping tree: TUI: %v\n", err)
		os.Exit(1)
	}

	// The screen is cleared on exit, so print the final state where it
	// stays in the scrollback.
	fmt.Print(model.renderTree())
	fmt.Println(model.statsLine(""))
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

// consumeStream folds events off the gRPC stream into m until it closes.
// The screen stays up after the run so results can be scrolled.
func consumeStream(stream rpcgrpc.PingService_StreamPingTreeClient, m *pingTreeModel, changed func()) {
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			m.finish(err)
			changed()
			return
		}
		if m.outputFile != nil {
			writeNDJSONLine(m.outputFile, ev)
		}
		m.applyEvent(ev)
		changed()
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

func classifyEvent(ev *rpcgrpc.PingTreeEvent) (string, any) { //nolint
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
// Model
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

	runStart time.Time

	// outputFile is the optional --output NDJSON tee. Owned by the
	// command; nil when not set.
	outputFile *os.File
}

func newPingTreeModel() *pingTreeModel {
	return &pingTreeModel{
		entries:  make(map[string]*treeEntry),
		levels:   make(map[int32]*levelInfo),
		runStart: time.Now(),
	}
}

// finish records the end of the stream, with err nil on a clean close.
func (m *pingTreeModel) finish(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamEnded = true
	m.streamErr = err
}

func (m *pingTreeModel) hint() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.streamEnded {
		return "Run complete — q to exit"
	}
	return "↑/↓ scroll | PgUp/PgDn page | a toggle auto-scroll | q quit"
}

// applyEvent folds one PingTreeEvent into the model state. Called
// from the stream consumer; takes the model's write lock so
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

// statsLine renders the summary above the tree:
// "Visors: A/B pinged, F failed | Elapsed: T | [status]".
func (m *pingTreeModel) statsLine(spin string) string {
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
			spin, m.statusPhase, m.statusInFly, m.statusPend)
	}
	if m.serverError != "" {
		stats += " | server error: " + m.serverError
	}
	if m.streamErr != nil {
		stats += " | stream error: " + m.streamErr.Error()
	}
	return stats
}

// renderTree builds the scrolling body. Per level we
// group entries by parentPK so each subtree is rendered as
//
//	<parent-PK>
//	├─ <child entry>
//	├─ <child entry>
//	└─ <child entry>
//
// Within each subtree entries are sorted: succeeded by ascending
// avg ms, then pending (discovery order), then failed. Final
// RunDone summary at the bottom.
func (m *pingTreeModel) renderTree() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.entryOrder) == 0 {
		return "Discovering network topology...\n"
	}

	// level → parentPK → ordered entries
	byLevel := make(map[int]map[string][]*treeEntry)
	for _, tpID := range m.entryOrder {
		e := m.entries[tpID]
		if _, ok := byLevel[e.level]; !ok {
			byLevel[e.level] = make(map[string][]*treeEntry)
		}
		byLevel[e.level][e.parentPK] = append(byLevel[e.level][e.parentPK], e)
	}

	levelKeys := make([]int, 0, len(byLevel))
	for k := range byLevel {
		levelKeys = append(levelKeys, k)
	}
	sort.Ints(levelKeys)

	headStyle := sgr(39, true)
	rootStyle := sgr(87, true)
	branchStyle := sgr(244, false)
	cacheStyle := sgr(117, false)
	liveStyle := sgr(82, false)
	failStyle := sgr(196, false)
	pendStyle := sgr(241, false)

	var sb strings.Builder

	for _, lv := range levelKeys {
		parents := byLevel[lv]
		totalEntries := 0
		for _, es := range parents {
			totalEntries += len(es)
		}

		levelHeader := fmt.Sprintf("=== Level %d (%d entries", lv, totalEntries)
		if info, ok := m.levels[int32(lv)]; ok && info.done { //nolint:gosec
			levelHeader += fmt.Sprintf(" — cached:%d live:%d failed:%d",
				info.skippedCached, info.succeeded-info.skippedCached, info.failed)
		}
		levelHeader += ") ==="
		sb.WriteString(headStyle(levelHeader))
		sb.WriteString("\n")

		// Stable subtree order: sort parent PKs alphabetically so the
		// frame-to-frame layout doesn't reshuffle when new entries arrive.
		parentKeys := make([]string, 0, len(parents))
		for pk := range parents {
			parentKeys = append(parentKeys, pk)
		}
		sort.Strings(parentKeys)

		for _, parentPK := range parentKeys {
			entries := parents[parentPK]

			sort.SliceStable(entries, func(i, j int) bool {
				ai, aj := entries[i], entries[j]
				cati := entryCategory(ai)
				catj := entryCategory(aj)
				if cati != catj {
					return cati < catj
				}
				if cati == 0 {
					return ai.pingAvgMs < aj.pingAvgMs
				}
				return ai.ts.Before(aj.ts)
			})

			rootLabel := rootStyle(parentPK)
			if lv == 1 {
				rootLabel += " " + branchStyle("(local)")
			}
			sb.WriteString(rootLabel)
			sb.WriteString("\n")

			for i, e := range entries {
				connector := "├─ "
				if i == len(entries)-1 {
					connector = "└─ "
				}
				line := formatEntryLine(e)
				switch entryCategory(e) {
				case 0:
					if e.latencySource == "transport_summary" {
						line = cacheStyle(line)
					} else {
						line = liveStyle(line)
					}
				case 1:
					line = pendStyle(line)
				case 2:
					line = failStyle(line)
				}
				sb.WriteString(branchStyle(connector))
				sb.WriteString(line)
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	if m.runDone != nil {
		sb.WriteString(headStyle("=== Run Summary ==="))
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf(
			"discovered=%d pinged=%d succeeded=%d failed=%d skipped_cached=%d\n"+
				"wall_time=%dms peak_in_flight=%d termination=%s\n",
			m.runDone.totalDiscovered, m.runDone.totalPinged,
			m.runDone.totalSucceeded, m.runDone.totalFailed,
			m.runDone.totalSkippedCached, m.runDone.wallMs,
			m.runDone.peakInFlight, m.runDone.terminationReason,
		))
	}
	return sb.String()
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

// formatEntryLine renders a single entry as one row. Full 66-char
// remotePK and full UUID tpID are emitted unredacted — PK truncation
// in operator-facing output is a hard no, and the tpID is the only
// stable handle for the (local, remote, transport-type) edge so we
// surface it alongside the PK.
//
// Layout (the renderer prepends ├─/└─ branch characters):
//
//	<glyph> <remotePK 66> <tpID 36> <tpType 6> [tag] <latency block | error>
func formatEntryLine(e *treeEntry) string {
	// glyph + status block
	var glyph, block string
	switch entryCategory(e) {
	case 1: // pending
		glyph = "·"
		block = "(pending)"
	case 2: // failed
		if e.canceled {
			glyph = "⊘"
		} else {
			glyph = "✗"
		}
		errMsg := e.setupErr
		if errMsg == "" {
			errMsg = e.pingErr
		}
		if errMsg == "" {
			errMsg = e.calcErr
		}
		// Cap the error message so a verbose setup-error doesn't
		// blow the row past its single-line budget. Visor-side
		// errors can be arbitrarily long (wrapped error chains,
		// raw remote responses); the row's fixed-width prefix is
		// ~113 chars, leaving ~85 chars for the payload before we
		// start needing horizontal scroll. The regression test in
		// tree_test.go pins total line width < 200 chars.
		const maxErrBlock = 80
		if len(errMsg) > maxErrBlock {
			errMsg = errMsg[:maxErrBlock-3] + "..."
		}
		block = errMsg
	default: // succeeded
		glyph = "✓"
		srcTag := "[live] "
		if e.latencySource == "transport_summary" {
			srcTag = "[cache]"
		}
		if e.sampleCount > 1 {
			block = fmt.Sprintf(
				"%s avg=%6.1fms p50=%6.1fms p99=%6.1fms jit=%5.1fms n=%d setup=%5.1fms",
				srcTag, e.pingAvgMs, e.pingP50Ms, e.pingP99Ms, e.jitterMs, e.sampleCount, e.setupLatencyMs,
			)
		} else {
			block = fmt.Sprintf(
				"%s avg=%6.1fms setup=%5.1fms n=%d",
				srcTag, e.pingAvgMs, e.setupLatencyMs, e.sampleCount,
			)
		}
	}

	// %-66s remotePK, %-36s tpID, %-6s tpType — fixed widths so the
	// columns line up across rows. SGR color escapes are added
	// downstream around the whole row, so width math stays correct.
	return fmt.Sprintf("%s %-66s %-36s %-6s %s",
		glyph, e.remotePK, e.tpID, e.tpType, block)
}
