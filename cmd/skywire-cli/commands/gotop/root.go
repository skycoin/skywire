// Package cligotop cmd/skywire-cli/commands/gotop/root.go
package cligotop

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VictoriaMetrics/metrics"
	jj "github.com/cloudfoundry-attic/jibber_jabber"
	ui "github.com/gizak/termui/v3"
	"github.com/spf13/cobra"
	"github.com/xxxserxxx/lingo/v2"

	"github.com/xxxserxxx/gotop/v4"
	"github.com/xxxserxxx/gotop/v4/colorschemes"
	"github.com/xxxserxxx/gotop/v4/devices"
	"github.com/xxxserxxx/gotop/v4/layout"
	w "github.com/xxxserxxx/gotop/v4/widgets"
)

const (
	graphHorizontalScaleDelta = 3
	defaultUI                 = "2:cpu\ndisk/1 2:mem/2\ntemp\n2:net 2:procs"
	minimalUI                 = "cpu\nmem procs"
	batteryUI                 = "cpu/2 batt/1\ndisk/1 2:mem/2\ntemp\nnet procs"
	procsUI                   = "cpu 4:procs\ndisk\nmem\nnet"
	kitchensink               = "3:cpu/2 3:mem/1\n4:temp/1 3:disk/2\npower\n3:net 3:procs"
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
}

// RootCmd is the gotop command
var RootCmd = &cobra.Command{
	Use:   "gotop",
	Short: "Terminal based graphical activity monitor",
	Long:  "A terminal based graphical activity monitor inspired by gtop and vtop",
	Run: func(_ *cobra.Command, _ []string) {
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

func run() error {
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
