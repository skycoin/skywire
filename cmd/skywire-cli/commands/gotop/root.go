// Package cligotop cmd/skywire-cli/commands/gotop/root.go
package cligotop

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/VictoriaMetrics/metrics"
	jj "github.com/cloudfoundry-attic/jibber_jabber"
	ui "github.com/gizak/termui/v3"
	"github.com/spf13/cobra"
	"github.com/xxxserxxx/gotop/v4"
	"github.com/xxxserxxx/gotop/v4/colorschemes"
	"github.com/xxxserxxx/gotop/v4/devices"
	"github.com/xxxserxxx/gotop/v4/layout"
	w "github.com/xxxserxxx/gotop/v4/widgets"
	"github.com/xxxserxxx/lingo/v2"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

const (
	graphHorizontalScaleDelta = 3
	defaultUI                 = "2:cpu\ndisk/1 2:mem/2\ntemp\n2:net 2:procs"
	minimalUI                 = "cpu\nmem procs"
	batteryUI                 = "cpu/2 batt/1\ndisk/1 2:mem/2\ntemp\nnet procs"
	procsUI                   = "cpu 4:procs\ndisk\nmem\nnet"
	kitchensink               = "3:cpu/2 3:mem/1\n4:temp/1 3:disk/2\npower\n3:net 3:procs"
	// remoteUI only shows widgets that support remote data: cpu, mem, temp
	remoteUI = "2:cpu\n2:mem/2\ntemp"
)

var (
	layoutFlag   string
	colorFlag    string
	rateFlag     string
	fahrenheit   bool
	percpu       bool
	averagecpu   bool
	statusbar    bool
	exportPort   string
	mbps         bool
	localMode    bool
	remotePK     string
	showProcs    bool
	procLimit    int
	onceMode     bool
	conf         gotop.Config
	help         *w.HelpMenu
	bar          *w.StatusBar
	stderrLogger = log.New(os.Stderr, "", 0)
	tr           lingo.Translations
)

func init() {
	RootCmd.Flags().StringVarP(&layoutFlag, "layout", "l", "default", "layout: default, minimal, battery, procs, kitchensink")
	RootCmd.Flags().StringVarP(&colorFlag, "color", "c", "default", "color scheme")
	RootCmd.Flags().StringVarP(&rateFlag, "rate", "r", "1s", "update interval")
	RootCmd.Flags().BoolVar(&fahrenheit, "fahrenheit", false, "use fahrenheit for temperature")
	RootCmd.Flags().BoolVarP(&percpu, "percpu", "p", false, "show per-cpu usage")
	RootCmd.Flags().BoolVarP(&averagecpu, "averagecpu", "a", false, "show average CPU usage")
	RootCmd.Flags().BoolVarP(&statusbar, "statusbar", "s", false, "show status bar")
	RootCmd.Flags().StringVarP(&exportPort, "export", "x", "", "export metrics on port (e.g., :8080)")
	RootCmd.Flags().BoolVar(&mbps, "mbps", false, "show network in Mbps")
	RootCmd.Flags().BoolVar(&localMode, "local", false, "run gotop directly using local gopsutil (skip visor gRPC)")
	RootCmd.Flags().StringVar(&remotePK, "remote", "", "connect to remote visor by public key (uses local visor's DMSG)")
	RootCmd.Flags().BoolVar(&showProcs, "procs", true, "include processes in remote stats")
	RootCmd.Flags().IntVarP(&procLimit, "proc-limit", "n", 10, "number of processes to show in remote mode")
	RootCmd.Flags().BoolVar(&onceMode, "once", false, "show single snapshot and exit (text mode, remote only)")
}

// RootCmd is the gotop command
var RootCmd = &cobra.Command{
	Use:   "gotop",
	Short: "Terminal based graphical activity monitor",
	Long: `A terminal based graphical activity monitor inspired by gtop and vtop.

By default, tries to connect to the local visor's gRPC server for system stats.
If unavailable, falls back to running gotop directly using local gopsutil.`,
	Run: func(cmd *cobra.Command, _ []string) {
		updateInterval, err := time.ParseDuration(rateFlag)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid rate: %v", err))
		}

		// Remote mode: connect to remote visor via DMSG through local visor's gRPC
		if remotePK != "" {
			if err := runRemoteMode(cmd, updateInterval); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}

		// Local mode flag: run gotop directly
		if localMode {
			if err := runDirectGotop(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}

		// Default: try visor gRPC first, fall back to direct gotop
		grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
		if err != nil {
			// gRPC not available, fall back to direct gotop
			stderrLogger.Printf("Visor gRPC not available, running gotop directly: %v", err)
			if err := runDirectGotop(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}
		defer grpcClient.Close() //nolint:errcheck

		// Try to get initial stats to verify gRPC is working
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = grpcClient.GetSystemStats(ctx, false, 0)
		cancel()
		if err != nil {
			// Close is handled by defer above
			stderrLogger.Printf("Visor gRPC available but GetSystemStats failed, running gotop directly: %v", err)
			if err := runDirectGotop(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}

		// gRPC is working, use it
		if err := runVisorGotop(grpcClient, updateInterval); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
	},
}

// runDirectGotop runs gotop directly using local gopsutil
func runDirectGotop() error {
	// Initialize translations
	ling, err := lingo.New("en_US", ".", gotop.Dicts)
	if err != nil {
		return fmt.Errorf("failed to load language files: %w", err)
	}
	lang, err := jj.DetectIETF()
	if err != nil {
		lang = "en_US"
	}
	lang = strings.Replace(lang, "-", "_", -1)
	tr = ling.TranslationsForLocale(lang)
	colorschemes.SetTr(tr)

	// Initialize config
	conf = gotop.NewConfig()
	conf.Tr = tr

	// Apply flags
	conf.Layout = layoutFlag
	conf.PercpuLoad = percpu
	conf.AverageLoad = averagecpu
	conf.Statusbar = statusbar
	conf.ExportPort = exportPort
	conf.Mbps = mbps

	if fahrenheit {
		conf.TempScale = 'F'
	} else {
		conf.TempScale = 'C'
	}

	if upInt, err := time.ParseDuration(rateFlag); err == nil {
		conf.UpdateInterval = upInt
	} else {
		return fmt.Errorf("invalid rate: %s", rateFlag)
	}

	if colorFlag != "" && colorFlag != "default" {
		cs, err := colorschemes.FromName(conf.ConfigDir, colorFlag)
		if err != nil {
			return fmt.Errorf("colorscheme error: %w", err)
		}
		conf.Colorscheme = cs
	}

	// Initialize devices
	for _, err := range devices.Startup(conf.ExtensionVars) {
		stderrLogger.Print(err)
	}

	// Get layout
	lstream := getLayout(conf)
	ly := layout.ParseLayout(lstream)

	// Initialize UI
	if err = ui.Init(); err != nil {
		return err
	}
	defer ui.Close()

	setDefaultTermuiColors(conf)
	help = w.NewHelpMenu(tr)
	if conf.Statusbar {
		bar = w.NewStatusBar()
	}

	grid, err := layout.Layout(ly, conf)
	if err != nil {
		return err
	}

	termWidth, termHeight := ui.TerminalDimensions()
	if conf.Statusbar {
		grid.SetRect(0, 0, termWidth, termHeight-1)
	} else {
		grid.SetRect(0, 0, termWidth, termHeight)
	}
	help.Resize(termWidth, termHeight)

	ui.Render(grid)
	if conf.Statusbar {
		bar.SetRect(0, termHeight-1, termWidth, termHeight)
		ui.Render(bar)
	}

	// Start metrics server if requested
	if conf.ExportPort != "" {
		go func() {
			http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
				metrics.WritePrometheus(w, true)
			})
			if err := http.ListenAndServe(conf.ExportPort, nil); err != nil { //nolint:gosec
				stderrLogger.Printf("metrics server error: %v", err)
			}
		}()
	}

	eventLoop(conf, grid)
	return nil
}

// runVisorGotop runs gotop using data from the local visor's gRPC
func runVisorGotop(grpcClient *rpcgrpc.PingClient, updateInterval time.Duration) error {
	// Get hostname for device naming
	ctx := context.Background()
	hostname := "Visor"
	stats, err := grpcClient.GetSystemStats(ctx, false, 0)
	if err == nil && stats.Host != nil && stats.Host.Hostname != "" {
		hostname = stats.Host.Hostname
	}

	// Set up gRPC device extension (registers with gotop's device system)
	if err := SetupGRPCDevice(grpcClient, hostname, updateInterval, showProcs, int32(procLimit)); err != nil { //nolint:gosec
		return fmt.Errorf("failed to setup gRPC device: %v", err)
	}
	defer ShutdownGRPCDevice()

	// Run gotop with the gRPC device
	return runGotopWithConfig(updateInterval, false)
}

// runRemoteMode connects to a remote visor over DMSG via local visor's gRPC
func runRemoteMode(_ *cobra.Command, updateInterval time.Duration) error {
	// Connect to local visor's gRPC
	grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		return fmt.Errorf("failed to connect to local visor gRPC: %v", err)
	}
	defer grpcClient.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Single snapshot mode (text output)
	if onceMode {
		return runRemoteOnce(ctx, grpcClient, updateInterval)
	}

	// Set up gRPC device extension that fetches from remote visor
	if err := SetupRemoteGRPCDevice(grpcClient, remotePK, updateInterval, showProcs, int32(procLimit)); err != nil { //nolint:gosec
		return fmt.Errorf("failed to setup remote gRPC device: %v", err)
	}
	defer ShutdownGRPCDevice()

	// Run gotop with remote-only layout (cpu, mem, temp only)
	return runGotopWithConfig(updateInterval, true)
}

// runRemoteOnce shows a single text snapshot from remote visor
func runRemoteOnce(ctx context.Context, grpcClient *rpcgrpc.PingClient, updateInterval time.Duration) error {
	// Create a channel to receive the first stats update
	statsCh := make(chan *rpcgrpc.SystemStats, 1)
	errCh := make(chan error, 1)

	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	go func() {
		err := grpcClient.StreamRemoteSystemStats(ctxTimeout, remotePK, updateInterval, showProcs, int32(procLimit), func(stats *rpcgrpc.SystemStats) { //nolint:gosec
			select {
			case statsCh <- stats:
			default:
			}
		})
		if err != nil {
			errCh <- err
		}
	}()

	select {
	case stats := <-statsCh:
		displayStatsText(stats)
		return nil
	case err := <-errCh:
		return fmt.Errorf("failed to get remote stats: %v", err)
	case <-ctxTimeout.Done():
		return fmt.Errorf("timeout waiting for remote stats")
	}
}

// runGotopWithConfig runs the gotop TUI with current configuration
func runGotopWithConfig(updateInterval time.Duration, remoteMode bool) error {
	// Initialize translations
	ling, err := lingo.New("en_US", ".", gotop.Dicts)
	if err != nil {
		return fmt.Errorf("failed to load language files: %w", err)
	}
	lang, err := jj.DetectIETF()
	if err != nil {
		lang = "en_US"
	}
	lang = strings.Replace(lang, "-", "_", -1)
	tr = ling.TranslationsForLocale(lang)
	colorschemes.SetTr(tr)

	// Initialize config
	conf = gotop.NewConfig()
	conf.Tr = tr

	// Apply flags
	if remoteMode {
		// Force remote layout for remote mode (only shows cpu, mem, temp)
		conf.Layout = "remote"
	} else {
		conf.Layout = layoutFlag
	}
	conf.PercpuLoad = percpu
	conf.AverageLoad = averagecpu
	conf.Statusbar = statusbar
	conf.ExportPort = exportPort
	conf.Mbps = mbps
	conf.UpdateInterval = updateInterval

	if fahrenheit {
		conf.TempScale = 'F'
	} else {
		conf.TempScale = 'C'
	}

	if colorFlag != "" && colorFlag != "default" {
		cs, err := colorschemes.FromName(conf.ConfigDir, colorFlag)
		if err != nil {
			return fmt.Errorf("colorscheme error: %w", err)
		}
		conf.Colorscheme = cs
	}

	// Initialize devices (gRPC devices are already registered)
	for _, err := range devices.Startup(conf.ExtensionVars) {
		stderrLogger.Print(err)
	}

	// Get layout
	lstream := getLayout(conf)
	ly := layout.ParseLayout(lstream)

	// Initialize UI
	if err = ui.Init(); err != nil {
		return err
	}
	defer ui.Close()

	setDefaultTermuiColors(conf)
	help = w.NewHelpMenu(tr)
	if conf.Statusbar {
		bar = w.NewStatusBar()
	}

	grid, err := layout.Layout(ly, conf)
	if err != nil {
		return err
	}

	termWidth, termHeight := ui.TerminalDimensions()
	if conf.Statusbar {
		grid.SetRect(0, 0, termWidth, termHeight-1)
	} else {
		grid.SetRect(0, 0, termWidth, termHeight)
	}
	help.Resize(termWidth, termHeight)

	ui.Render(grid)
	if conf.Statusbar {
		bar.SetRect(0, termHeight-1, termWidth, termHeight)
		ui.Render(bar)
	}

	// Start metrics server if requested
	if conf.ExportPort != "" {
		go func() {
			http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
				metrics.WritePrometheus(w, true)
			})
			if err := http.ListenAndServe(conf.ExportPort, nil); err != nil { //nolint:gosec
				stderrLogger.Printf("metrics server error: %v", err)
			}
		}()
	}

	eventLoop(conf, grid)
	return nil
}

func setDefaultTermuiColors(c gotop.Config) {
	ui.Theme.Default = ui.NewStyle(ui.Color(c.Colorscheme.Fg), ui.Color(c.Colorscheme.Bg))
	ui.Theme.Block.Title = ui.NewStyle(ui.Color(c.Colorscheme.BorderLabel), ui.Color(c.Colorscheme.Bg))
	ui.Theme.Block.Border = ui.NewStyle(ui.Color(c.Colorscheme.BorderLine), ui.Color(c.Colorscheme.Bg))
}

func getLayout(conf gotop.Config) io.Reader {
	switch conf.Layout {
	case "default":
		return strings.NewReader(defaultUI)
	case "minimal":
		return strings.NewReader(minimalUI)
	case "battery":
		return strings.NewReader(batteryUI)
	case "procs":
		return strings.NewReader(procsUI)
	case "kitchensink":
		return strings.NewReader(kitchensink)
	case "remote":
		return strings.NewReader(remoteUI)
	default:
		return strings.NewReader(defaultUI)
	}
}

func eventLoop(c gotop.Config, grid *layout.MyGrid) {
	drawTicker := time.NewTicker(c.UpdateInterval).C

	sigTerm := make(chan os.Signal, 2)
	signal.Notify(sigTerm, os.Interrupt, syscall.SIGTERM)

	uiEvents := ui.PollEvents()
	previousKey := ""

	for {
		select {
		case <-sigTerm:
			return
		case <-drawTicker:
			if !c.HelpVisible {
				ui.Render(grid)
				if c.Statusbar {
					ui.Render(bar)
				}
			}
		case e := <-uiEvents:
			if grid.Proc != nil && grid.Proc.HandleEvent(e) {
				ui.Render(grid.Proc)
				break
			}
			switch e.ID {
			case "q", "<C-c>":
				return
			case "?":
				c.HelpVisible = !c.HelpVisible
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				termWidth, termHeight := payload.Width, payload.Height
				if c.Statusbar {
					grid.SetRect(0, 0, termWidth, termHeight-1)
					bar.SetRect(0, termHeight-1, termWidth, termHeight)
				} else {
					grid.SetRect(0, 0, payload.Width, payload.Height)
				}
				help.Resize(payload.Width, payload.Height)
				ui.Clear()
			}

			if c.HelpVisible {
				switch e.ID {
				case "?":
					ui.Clear()
					ui.Render(help)
				case "<Escape>":
					c.HelpVisible = false
					ui.Render(grid)
				case "<Resize>":
					ui.Render(help)
				}
			} else {
				switch e.ID {
				case "?":
					ui.Render(grid)
				case "h":
					c.GraphHorizontalScale += graphHorizontalScaleDelta
					for _, item := range grid.Lines {
						item.Scale(c.GraphHorizontalScale)
					}
					ui.Render(grid)
				case "l":
					if c.GraphHorizontalScale > graphHorizontalScaleDelta {
						c.GraphHorizontalScale -= graphHorizontalScaleDelta
						for _, item := range grid.Lines {
							item.Scale(c.GraphHorizontalScale)
							ui.Render(item)
						}
					}
				case "b":
					if grid.Net != nil {
						grid.Net.Mbps = !grid.Net.Mbps
					}
				case "<Resize>":
					ui.Render(grid)
					if c.Statusbar {
						ui.Render(bar)
					}
				case "<MouseLeft>":
					if grid.Proc != nil {
						payload := e.Payload.(ui.Mouse)
						grid.Proc.HandleClick(payload.X, payload.Y)
						ui.Render(grid.Proc)
					}
				case "k", "<Up>", "<MouseWheelUp>":
					if grid.Proc != nil {
						grid.Proc.ScrollUp()
						ui.Render(grid.Proc)
					}
				case "j", "<Down>", "<MouseWheelDown>":
					if grid.Proc != nil {
						grid.Proc.ScrollDown()
						ui.Render(grid.Proc)
					}
				case "<Home>":
					if grid.Proc != nil {
						grid.Proc.ScrollTop()
						ui.Render(grid.Proc)
					}
				case "g":
					if grid.Proc != nil {
						if previousKey == "g" {
							grid.Proc.ScrollTop()
							ui.Render(grid.Proc)
						}
					}
				case "G", "<End>":
					if grid.Proc != nil {
						grid.Proc.ScrollBottom()
						ui.Render(grid.Proc)
					}
				case "<C-d>":
					if grid.Proc != nil {
						grid.Proc.ScrollHalfPageDown()
						ui.Render(grid.Proc)
					}
				case "<C-u>":
					if grid.Proc != nil {
						grid.Proc.ScrollHalfPageUp()
						ui.Render(grid.Proc)
					}
				case "<C-f>", "<PageDown>":
					if grid.Proc != nil {
						grid.Proc.ScrollPageDown()
						ui.Render(grid.Proc)
					}
				case "<C-b>", "<PageUp>":
					if grid.Proc != nil {
						grid.Proc.ScrollPageUp()
						ui.Render(grid.Proc)
					}
				case "d":
					if grid.Proc != nil {
						if previousKey == "d" {
							grid.Proc.KillProc("SIGTERM")
						}
					}
				case "3":
					if grid.Proc != nil {
						if previousKey == "d" {
							grid.Proc.KillProc("SIGQUIT")
						}
					}
				case "9":
					if grid.Proc != nil {
						if previousKey == "d" {
							grid.Proc.KillProc("SIGKILL")
						}
					}
				case "<Tab>":
					if grid.Proc != nil {
						grid.Proc.ToggleShowingGroupedProcs()
						ui.Render(grid.Proc)
					}
				case "m", "c", "n", "p":
					if grid.Proc != nil {
						grid.Proc.ChangeProcSortMethod(w.ProcSortMethod(e.ID))
						ui.Render(grid.Proc)
					}
				case "/":
					if grid.Proc != nil {
						grid.Proc.SetEditingFilter(true)
						ui.Render(grid.Proc)
					}
				}

				if previousKey == e.ID {
					previousKey = ""
				} else {
					previousKey = e.ID
				}
			}
		}
	}
}

// displayStatsText shows a single snapshot in text mode (for --once flag)
func displayStatsText(stats *rpcgrpc.SystemStats) {
	if stats.Error != "" {
		fmt.Printf("Error: %s\n", stats.Error)
		return
	}

	// Host info
	if stats.Host != nil {
		fmt.Printf("Host: %s (%s %s)\n", stats.Host.Hostname, stats.Host.Platform, stats.Host.PlatformVersion)
		fmt.Printf("Kernel: %s (%s)\n", stats.Host.KernelVersion, stats.Host.KernelArch)
		fmt.Printf("Uptime: %s | CPUs: %d\n\n", formatDuration(time.Duration(stats.Host.UptimeSec)*time.Second), stats.Host.NumCpus)
	}

	// CPU
	fmt.Printf("CPU Average: %.1f%%\n", stats.CpuAverage)
	for _, cpu := range stats.Cpu {
		fmt.Printf("  %s: %.1f%%\n", cpu.Cpu, cpu.UsagePercent)
	}
	fmt.Println()

	// Memory
	if stats.Memory != nil {
		fmt.Printf("Memory: %s / %s (%.1f%%)\n",
			formatBytes(stats.Memory.Used), formatBytes(stats.Memory.Total), stats.Memory.UsedPercent)
	}
	if stats.Swap != nil && stats.Swap.Total > 0 {
		fmt.Printf("Swap:   %s / %s (%.1f%%)\n",
			formatBytes(stats.Swap.Used), formatBytes(stats.Swap.Total), stats.Swap.UsedPercent)
	}
	fmt.Println()

	// Network
	if stats.Network != nil {
		fmt.Printf("Network: TX %s (%s/s) | RX %s (%s/s)\n",
			formatBytes(stats.Network.BytesSent), formatBytes(uint64(stats.Network.BytesSentRate)),
			formatBytes(stats.Network.BytesRecv), formatBytes(uint64(stats.Network.BytesRecvRate)))
		fmt.Println()
	}

	// Disks
	if len(stats.Disks) > 0 {
		fmt.Println("Disks:")
		for _, d := range stats.Disks {
			if d.Total > 0 {
				fmt.Printf("  %-25s %s / %s (%.1f%%)\n",
					d.Mountpoint, formatBytes(d.Used), formatBytes(d.Total), d.UsedPercent)
			}
		}
		fmt.Println()
	}

	// Temperatures
	if len(stats.Temps) > 0 {
		fmt.Println("Temperatures:")
		for _, t := range stats.Temps {
			fmt.Printf("  %-25s %.1f°C\n", t.SensorKey, t.Temperature)
		}
		fmt.Println()
	}

	// Processes
	if len(stats.Processes) > 0 {
		fmt.Println("Top Processes:")
		fmt.Printf("  %-7s %-20s %7s %7s %10s %-10s\n", "PID", "NAME", "CPU%", "MEM%", "RSS", "USER")
		// Sort by CPU
		procs := stats.Processes
		sort.Slice(procs, func(i, j int) bool {
			return procs[i].CpuPercent > procs[j].CpuPercent
		})
		for _, p := range procs {
			fmt.Printf("  %-7d %-20s %7.1f %7.1f %10s %-10s\n",
				p.Pid, truncate(p.Name, 20), p.CpuPercent, p.MemoryPercent,
				formatBytes(p.MemoryRss), truncate(p.Username, 10))
		}
	}
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "."
}
