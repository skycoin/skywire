// Package clivisor cmd/skywire-cli/commands/visor/hvtui.go c4-vis-cli
package clivisor

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	hvCmd.AddCommand(hvTUICmd)
}

const hvKeys = "q:quit r:refresh enter:detail esc:back  m/M:hops/mux c:calc-rt w:reward p:autoconn  s/S/A/l:app(toggle/stop/autostart/logs)  T/t:tp+/-  x:rm-rule  h:health G:dmsg-conn N:dmsg-count  P:proxies f:ports  R:reload D:shutdown"

var (
	hvHeadStyle   = tcell.StyleDefault.Foreground(color.Yellow)
	hvDimStyle    = tcell.StyleDefault.Foreground(color.Gray)
	hvBorderStyle = tcell.StyleDefault.Foreground(color.Gray)
	hvFocusStyle  = tcell.StyleDefault.Foreground(color.Aqua)
)

var hvTUICmd = &cobra.Command{
	Use:   "tui",
	Short: "Hypervisor terminal UI",
	Long: `Interactive terminal UI for managing visors connected to this hypervisor.

Shows all connected visors with version, uptime, transports, and apps.
Select a visor to see detailed info. Press 'r' to refresh, 'q' to quit.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		app, err := progkit.Open()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		defer app.Close()

		// --- State, touched only on the draw goroutine ---
		var (
			visors      []visorapi.HVVisorEntry
			sel, top    int
			detailFocus bool
			status      = "Loading..."
			quit        bool
			cur         *hvModal
			detail      = &progkit.Text{ID: "detail", Selectable: true}
		)
		detail.SetANSI(markup("[gray]Select a visor to view details"))

		// queue runs fn on the draw goroutine, from any goroutine.
		var (
			queueMu sync.Mutex
			queued  []func()
		)
		queue := func(fn func()) {
			queueMu.Lock()
			queued = append(queued, fn)
			queueMu.Unlock()
			app.Redraw()
		}
		drain := func() {
			queueMu.Lock()
			fns := queued
			queued = nil
			queueMu.Unlock()
			for _, fn := range fns {
				fn()
			}
		}
		setStatus := func(msg string) {
			queue(func() { status = msg })
		}

		// refresh is defined further down but referenced by helpers above; predeclare.
		var refresh func()

		// --- Modal helpers ---
		showModal := func(m *hvModal) { cur = m }
		closeModal := func() { cur = nil }
		showInputModal := func(title, label, defaultValue string, onSubmit func(value string)) {
			in := &progkit.Input{ID: "modal-input"}
			in.SetValue(defaultValue)
			in.OnSubmit = func(v string) {
				closeModal()
				onSubmit(strings.TrimSpace(v))
			}
			showModal(&hvModal{title: title, w: 80, h: 6,
				draw: func(f *progkit.Frame, r progkit.Rect) {
					progkit.DrawText(f.Screen, r.X, r.Y, r.W, label, hvHeadStyle)
					in.Draw(f, progkit.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: 1}, true)
					progkit.DrawText(f.Screen, r.X, r.Y+3, r.W, "enter ok  esc cancel", hvDimStyle)
				},
				key: func(ev *tcell.EventKey) { in.Key(ev) },
			})
		}
		showChoiceModal := func(title, message string, choices []string, onDone func(label string)) {
			lines := wrapText(message, 76)
			// The last choice is No or Cancel, so a stray Enter does nothing.
			focus := len(choices) - 1
			buttons := make([]*progkit.Button, len(choices))
			for i, c := range choices {
				label := c
				buttons[i] = &progkit.Button{ID: fmt.Sprintf("choice-%d", i), Label: label, OnPress: func() {
					closeModal()
					onDone(label)
				}}
			}
			showModal(&hvModal{title: title, w: 80, h: len(lines) + 4,
				draw: func(f *progkit.Frame, r progkit.Rect) {
					for i, l := range lines {
						progkit.DrawText(f.Screen, r.X, r.Y+i, r.W, l, tcell.StyleDefault)
					}
					x := r.X
					for i, b := range buttons {
						b.Draw(f, progkit.Rect{X: x, Y: r.Y + len(lines) + 1, W: r.X + r.W - x, H: 1}, i == focus)
						x += len(b.Label) + 6
					}
				},
				key: func(ev *tcell.EventKey) {
					switch ev.Key() {
					case tcell.KeyLeft, tcell.KeyBacktab:
						focus = (focus + len(buttons) - 1) % len(buttons)
					case tcell.KeyRight, tcell.KeyTab:
						focus = (focus + 1) % len(buttons)
					default:
						buttons[focus].Key(ev)
					}
				},
			})
		}
		showConfirmModal := func(title, message string, onConfirm func()) {
			showChoiceModal(title, message, []string{"Yes", "No"}, func(label string) {
				if label == "Yes" {
					onConfirm()
				}
			})
		}
		showListModal := func(title string, items []string, onSelect func(idx int)) {
			if len(items) == 0 {
				showConfirmModal(title, "No items available.", func() {})
				return
			}
			list := &progkit.List{ID: "modal-list", Items: items}
			list.OnActivate = func(idx int) {
				closeModal()
				onSelect(idx)
			}
			showModal(&hvModal{title: title, w: 110, h: min(len(items)+2, 24),
				draw: func(f *progkit.Frame, r progkit.Rect) { list.Draw(f, r, true) },
				key:  func(ev *tcell.EventKey) { list.Key(ev) },
			})
		}
		showTextModal := func(title, text string, w, h int) {
			view := &progkit.Text{ID: "modal-text", Selectable: true}
			view.SetANSI(markup(text))
			showModal(&hvModal{title: title, w: w, h: h,
				draw: func(f *progkit.Frame, r progkit.Rect) { view.Draw(f, r) },
				key:  func(ev *tcell.EventKey) { view.Key(ev) },
			})
		}
		showFormModal := func(title string, fields []*hvField, onOK func()) {
			focus := 0
			for i, fl := range fields {
				if fl.input != nil {
					fl.input.ID = fmt.Sprintf("field-%d", i)
				}
			}
			ok := &progkit.Button{ID: "form-ok", Label: "OK", OnPress: func() {
				closeModal()
				onOK()
			}}
			cancel := &progkit.Button{ID: "form-cancel", Label: "Cancel", OnPress: closeModal}
			n := len(fields) + 2
			showModal(&hvModal{title: title, w: 80, h: len(fields) + 4,
				draw: func(f *progkit.Frame, r progkit.Rect) {
					for i, fl := range fields {
						y := r.Y + i
						st := tcell.StyleDefault
						if i == focus {
							st = hvFocusStyle
						}
						x := r.X + progkit.DrawText(f.Screen, r.X, y, r.W, fmt.Sprintf("%-34s ", fl.label), st)
						rest := progkit.Rect{X: x, Y: y, W: r.X + r.W - x, H: 1}
						switch {
						case fl.input != nil:
							fl.input.Draw(f, rest, i == focus)
						case fl.choices != nil:
							progkit.DrawText(f.Screen, x, y, rest.W, "< "+fl.choices[fl.choice]+" >", st)
						default:
							mark := "[ ]"
							if fl.on {
								mark = "[x]"
							}
							progkit.DrawText(f.Screen, x, y, rest.W, mark, st)
						}
					}
					y := r.Y + len(fields) + 1
					ok.Draw(f, progkit.Rect{X: r.X, Y: y, W: 8, H: 1}, focus == n-2)
					cancel.Draw(f, progkit.Rect{X: r.X + 10, Y: y, W: 12, H: 1}, focus == n-1)
				},
				key: func(ev *tcell.EventKey) {
					switch ev.Key() {
					case tcell.KeyTab, tcell.KeyDown:
						focus = (focus + 1) % n
						return
					case tcell.KeyBacktab, tcell.KeyUp:
						focus = (focus + n - 1) % n
						return
					}
					if focus >= len(fields) {
						[]*progkit.Button{ok, cancel}[focus-len(fields)].Key(ev)
						return
					}
					fl := fields[focus]
					switch {
					case fl.input != nil:
						if ev.Key() == tcell.KeyEnter {
							focus++
							return
						}
						fl.input.Key(ev)
					case fl.choices != nil:
						switch {
						case ev.Key() == tcell.KeyLeft:
							fl.choice = (fl.choice + len(fl.choices) - 1) % len(fl.choices)
						case ev.Key() == tcell.KeyRight, ev.Key() == tcell.KeyEnter, progkit.Typed(ev) == " ":
							fl.choice = (fl.choice + 1) % len(fl.choices)
						}
					default:
						if ev.Key() == tcell.KeyEnter || progkit.Typed(ev) == " " {
							fl.on = !fl.on
						}
					}
				},
			})
		}
		fetchSummary := func(pk cipher.PubKey) (*visorapi.Summary, error) {
			return rpcClient.HVVisorSummary(pk)
		}
		showRegisterForwardedPortModal := func(targetPK cipher.PubKey) {
			port, local, label, desc, proxy := textField("port (remote)", ""), textField("local port", ""), textField("label", ""), textField("description", ""), textField("proxy_addr (host:port, optional)", "")
			skynetEn, dmsgEn, landing := &hvField{label: "skynet", on: true}, &hvField{label: "dmsg"}, &hvField{label: "show on landing", on: true}
			showFormModal("Register forwarded port on "+targetPK.String(),
				[]*hvField{port, local, label, desc, skynetEn, dmsgEn, landing, proxy},
				func() {
					p, perr := strconv.Atoi(strings.TrimSpace(port.input.Value()))
					if perr != nil {
						p = 0
					}
					l, lerr := strconv.Atoi(strings.TrimSpace(local.input.Value()))
					if lerr != nil {
						l = 0
					}
					if p <= 0 {
						setStatus("forwarded port: port required")
						return
					}
					fp := visorapi.ForwardedPort{
						Port:          p,
						LocalPort:     l,
						Label:         strings.TrimSpace(label.input.Value()),
						Description:   strings.TrimSpace(desc.input.Value()),
						ShowOnLanding: landing.on,
						Skynet:        skynetEn.on,
						DMSG:          dmsgEn.on,
						ProxyAddr:     strings.TrimSpace(proxy.input.Value()),
					}
					go func() {
						if err := rpcClient.HVRegisterForwardedPort(targetPK, fp); err != nil {
							setStatus("register fwd port failed: " + err.Error())
							return
						}
						setStatus(fmt.Sprintf("forwarded port %d registered on %s", p, targetPK.String()))
						refresh()
					}()
				})
		}
		showAddTransportModal := func(targetPK cipher.PubKey, onSubmit func(remote cipher.PubKey, tpType, label string)) {
			remote, label := textField("remote PK", ""), textField("label", "user")
			tpType := &hvField{label: "type", choices: []string{"stcpr", "sudph", "stcp", "dmsg"}}
			showFormModal("Add transport on "+targetPK.String(), []*hvField{remote, tpType, label}, func() {
				pkStr := strings.TrimSpace(remote.input.Value())
				if pkStr == "" {
					setStatus("add transport: remote PK required")
					return
				}
				var rpk cipher.PubKey
				if err := rpk.Set(pkStr); err != nil {
					setStatus("add transport: invalid PK: " + err.Error())
					return
				}
				onSubmit(rpk, tpType.choices[tpType.choice], strings.TrimSpace(label.input.Value()))
			})
		}

		// --- Visor table ---
		tableHeader := []string{"#", "PK", "VERSION", "UPTIME", "TP", "APPS", "IP", "CC", "STATUS"}
		tableRow := func(i int, e visorapi.HVVisorEntry) ([]string, tcell.Style) {
			st, stStyle := "ok", tcell.StyleDefault.Foreground(color.Green)
			if e.IsLocal {
				st, stStyle = "local", tcell.StyleDefault.Foreground(color.Aqua)
			}
			if e.ProxiedVia != nil {
				st, stStyle = "via "+e.ProxiedVia.String(), tcell.StyleDefault.Foreground(color.Yellow)
			}
			if e.Error != "" {
				st, stStyle = truncStr(e.Error, 20), tcell.StyleDefault.Foreground(color.Red)
			}
			up := "-"
			if e.Uptime > 0 {
				up = (time.Duration(e.Uptime) * time.Second).Truncate(time.Second).String()
			}
			return []string{strconv.Itoa(i + 1), e.PK.String(), dash(e.Version), up, strconv.Itoa(e.Transports),
				strconv.Itoa(e.Apps), dash(e.PublicIP), dash(e.CountryCode), st}, stStyle
		}
		// columns pads every cell but the last to its column's widest.
		columns := func(rows [][]string) []string {
			widths := make([]int, len(tableHeader))
			for _, r := range rows {
				for c, s := range r {
					widths[c] = max(widths[c], len(s))
				}
			}
			out := make([]string, len(rows))
			for i, r := range rows {
				var b strings.Builder
				for c, s := range r[:len(r)-1] {
					fmt.Fprintf(&b, "%-*s ", widths[c], s)
				}
				out[i] = b.String()
			}
			return out
		}
		var openDetail func()
		drawTable := func(f *progkit.Frame, r progkit.Rect, place bool) {
			if r.Empty() {
				return
			}
			rows := [][]string{tableHeader}
			styles := make([]tcell.Style, len(visors))
			for i, e := range visors {
				var cells []string
				cells, styles[i] = tableRow(i, e)
				rows = append(rows, cells)
			}
			lines := columns(rows)
			progkit.DrawText(f.Screen, r.X, r.Y, r.W, lines[0]+tableHeader[len(tableHeader)-1], hvHeadStyle)
			body := progkit.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
			sel = min(max(sel, 0), max(len(visors)-1, 0))
			top = min(top, sel)
			top = max(top, sel-body.H+1)
			items := make([]string, len(visors))
			for i := range visors {
				row, st, stStyle := lines[i+1], rows[i+1][len(tableHeader)-1], styles[i]
				items[i] = row + st
				if i < top || i >= top+body.H {
					continue
				}
				y := body.Y + i - top
				base := tcell.StyleDefault
				if i == sel {
					base, stStyle = base.Reverse(!detailFocus).Bold(true), stStyle.Reverse(!detailFocus).Bold(true)
				}
				progkit.Fill(f.Screen, progkit.Rect{X: body.X, Y: y, W: body.W, H: 1}, base)
				x := body.X + progkit.DrawText(f.Screen, body.X, y, body.W, row, base)
				progkit.DrawText(f.Screen, x, y, body.X+body.W-x, st, stStyle)
			}
			if !place {
				return
			}
			f.Place("visors", body, progkit.Element{Kind: "list", Items: items, Selected: sel, Focus: !detailFocus}, func(m progkit.Msg) {
				switch m.Type {
				case "select":
					sel = m.Index
				case "activate":
					sel = m.Index
					openDetail()
				}
			})
		}

		// --- Show detail for selected visor ---
		showDetail := func(e visorapi.HVVisorEntry) {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("[yellow]Public Key:[white] %s\n", e.PK.String()))
			sb.WriteString(fmt.Sprintf("[yellow]Version:[white]    %s\n", e.Version))
			sb.WriteString(fmt.Sprintf("[yellow]Build Tag:[white]  %s\n", e.BuildTag))
			sb.WriteString(fmt.Sprintf("[yellow]Config:[white]     %s\n", e.ConfigVersion))
			if e.Uptime > 0 {
				sb.WriteString(fmt.Sprintf("[yellow]Uptime:[white]     %s\n", (time.Duration(e.Uptime) * time.Second).Truncate(time.Second)))
			}
			sb.WriteString(fmt.Sprintf("[yellow]Local IP:[white]   %s\n", e.LocalIP))
			sb.WriteString(fmt.Sprintf("[yellow]Public IP:[white]  %s\n", e.PublicIP))
			sb.WriteString(fmt.Sprintf("[yellow]Country:[white]    %s\n", e.CountryCode))
			sb.WriteString(fmt.Sprintf("[yellow]NAT:[white]        %v\n", e.IsSymmetricNAT))
			sb.WriteString(fmt.Sprintf("[yellow]Transports:[white] %d\n", e.Transports))
			sb.WriteString(fmt.Sprintf("[yellow]Apps:[white]       %d\n", e.Apps))
			if e.RewardAddress != "" {
				addr := e.RewardAddress
				if len(addr) > 40 {
					addr = addr[:40] + "..."
				}
				sb.WriteString(fmt.Sprintf("[yellow]Reward:[white]     %s\n", addr))
			}
			if e.Error != "" {
				sb.WriteString(fmt.Sprintf("\n[red]Error:[white] %s\n", e.Error))
			}
			if e.IsLocal {
				sb.WriteString("\n[cyan](this is the local hypervisor visor)[white]\n")
			}

			var remotePK cipher.PubKey
			if err := remotePK.Set(e.PK.String()); err == nil {
				summary, err := rpcClient.HVVisorSummary(remotePK)
				if err == nil {
					if len(summary.Overview.Transports) > 0 {
						sb.WriteString("\n[yellow]── Transports ──[white]\n")
						for _, tp := range summary.Overview.Transports {
							tpID := tp.ID.String()
							if len(tpID) > 10 {
								tpID = tpID[:8] + ".."
							}
							sb.WriteString(fmt.Sprintf("  %s  %-6s  → %s  %s\n",
								tpID, strings.ToUpper(string(tp.Type)), tp.Remote.String(), tp.Label))
						}
					}
					if len(summary.Overview.Apps) > 0 {
						sb.WriteString("\n[yellow]── Apps ──[white]\n")
						for _, a := range summary.Overview.Apps {
							statusColor := "red"
							if a.Status == 1 {
								statusColor = "green"
							}
							sb.WriteString(fmt.Sprintf("  [%s]●[white] %-20s port:%d\n",
								statusColor, a.Name, a.Port))
						}
					}
					if len(summary.RouteGroups) > 0 {
						sb.WriteString("\n[yellow]── Route Groups ──[white]\n")
						for _, rg := range summary.RouteGroups {
							sb.WriteString(fmt.Sprintf("  [cyan]%s:%d[white] → [cyan]%s:%d[white]  fwd=%d csm=%d\n",
								rg.Desc.SrcPK, rg.Desc.SrcPort, rg.Desc.DstPK, rg.Desc.DstPort,
								rg.FwdRuleID, rg.ConsumeRuleID))
							for i, hop := range rg.Hops {
								tpID := hop.TpID
								if len(tpID) > 10 {
									tpID = tpID[:8] + ".."
								}
								sb.WriteString(fmt.Sprintf("    [gray]hop %d:[white] %s  %s  %s → %s\n",
									i+1, tpID, strings.ToUpper(hop.TpType), hop.From, hop.To))
							}
						}
					}
					if len(summary.DMSGServers) > 0 {
						sb.WriteString("\n[yellow]── DMSG Servers ──[white]\n")
						for _, ds := range summary.DMSGServers {
							lat := "-"
							if ds.Latency > 0 {
								lat = ds.Latency.Truncate(time.Millisecond).String()
							}
							sb.WriteString(fmt.Sprintf("  %s  lat=%s\n", ds.PK.String(), lat))
						}
					}
				}
			}
			queue(func() {
				detail.SetANSI(markup(sb.String()))
				detail.ScrollTo(0)
			})
		}

		// --- Refresh data ---
		refresh = func() {
			setStatus("Refreshing...")
			go func() {
				entries, err := rpcClient.HVListVisors()
				if err != nil {
					setStatus(fmt.Sprintf("Error: %s", err))
					return
				}
				queue(func() { visors = entries })
				setStatus(fmt.Sprintf("%d visors | refreshed %s", len(entries), time.Now().Format("15:04:05")))
			}()
		}

		// --- Selection handler ---
		openDetail = func() {
			if sel >= len(visors) {
				return
			}
			v, n := visors[sel], len(visors)
			detailFocus = true
			go func() {
				setStatus("Loading detail...")
				showDetail(v)
				setStatus(fmt.Sprintf("%d visors | detail view", n))
			}()
		}

		// --- Action helpers ---
		selectedVisor := func() (visorapi.HVVisorEntry, bool) {
			if sel < 0 || sel >= len(visors) {
				return visorapi.HVVisorEntry{}, false
			}
			return visors[sel], true
		}

		// --- Key handlers ---
		onKey := func(event *tcell.EventKey) *tcell.EventKey {
			switch event.Key() {
			case tcell.KeyEscape:
				detailFocus = false
				return nil
			case tcell.KeyRune:
				switch progkit.Typed(event) {
				case "q", "Q":
					quit = true
					return nil
				case "r":
					refresh()
					return nil
				case "m":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showInputModal("Set min_hops on "+v.PK.String(), "min_hops", "0", func(s string) {
						n, err := strconv.ParseUint(s, 10, 16)
						if err != nil {
							setStatus(fmt.Sprintf("min_hops: invalid number: %s", err))
							return
						}
						go func() {
							if err := rpcClient.HVSetMinHops(v.PK, uint16(n)); err != nil {
								setStatus("set min_hops failed: " + err.Error())
								return
							}
							setStatus(fmt.Sprintf("set min_hops=%d on %s", n, v.PK.String()))
							refresh()
						}()
					})
					return nil
				case "w":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showInputModal("Set reward address on "+v.PK.String(), "address", v.RewardAddress, func(s string) {
						go func() {
							if _, err := rpcClient.HVSetRewardAddress(v.PK, s); err != nil {
								setStatus("set reward failed: " + err.Error())
								return
							}
							setStatus("reward address set on " + v.PK.String())
							refresh()
						}()
					})
					return nil
				case "s":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading apps...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch apps failed: " + err.Error())
							return
						}
						apps := sum.Overview.Apps
						labels := make([]string, len(apps))
						for i, a := range apps {
							state := "stopped"
							if a.Status == 1 {
								state = "running"
							}
							labels[i] = fmt.Sprintf("[%-7s] %-22s port:%d", state, a.Name, a.Port)
						}
						queue(func() {
							showListModal("Toggle app on "+v.PK.String(), labels, func(idx int) {
								a := apps[idx]
								go func() {
									var aerr error
									if a.Status == 1 {
										aerr = rpcClient.HVStopApp(v.PK, a.Name)
									} else {
										aerr = rpcClient.HVStartApp(v.PK, a.Name)
									}
									if aerr != nil {
										setStatus(a.Name + ": " + aerr.Error())
										return
									}
									setStatus("toggled " + a.Name + " on " + v.PK.String())
									refresh()
								}()
							})
						})
					}()
					return nil
				case "S":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading apps...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch apps failed: " + err.Error())
							return
						}
						running := []*appserver.AppState{}
						for _, a := range sum.Overview.Apps {
							if a.Status == 1 {
								running = append(running, a)
							}
						}
						labels := make([]string, len(running))
						for i, a := range running {
							labels[i] = fmt.Sprintf("%-22s port:%d", a.Name, a.Port)
						}
						queue(func() {
							showListModal("Stop app on "+v.PK.String(), labels, func(idx int) {
								name := running[idx].Name
								go func() {
									if err := rpcClient.HVStopApp(v.PK, name); err != nil {
										setStatus("stop " + name + " failed: " + err.Error())
										return
									}
									setStatus("stopped " + name + " on " + v.PK.String())
									refresh()
								}()
							})
						})
					}()
					return nil
				case "t":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading transports...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch transports failed: " + err.Error())
							return
						}
						tps := sum.Overview.Transports
						labels := make([]string, len(tps))
						for i, tp := range tps {
							short := tp.ID.String() + ".."
							labels[i] = fmt.Sprintf("%s  %-6s  %s..  %s", short, strings.ToUpper(string(tp.Type)), tp.Remote.String(), tp.Label)
						}
						queue(func() {
							showListModal("Delete transport on "+v.PK.String(), labels, func(idx int) {
								tp := tps[idx]
								short := tp.ID.String() + ".."
								showConfirmModal("Confirm",
									fmt.Sprintf("Delete transport %s (%s) ?", short, tp.Type),
									func() {
										go func() {
											if err := rpcClient.HVRemoveTransport(v.PK, tp.ID); err != nil {
												setStatus("rm transport failed: " + err.Error())
												return
											}
											setStatus("transport " + short + " removed on " + v.PK.String())
											refresh()
										}()
									})
							})
						})
					}()
					return nil
				case "x":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading route groups...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch route groups failed: " + err.Error())
							return
						}
						rgs := sum.RouteGroups
						if len(rgs) == 0 {
							queue(func() {
								showInputModal("Delete rule by ID on "+v.PK.String(),
									"route ID", "", func(s string) {
										n, err := strconv.ParseUint(s, 10, 32)
										if err != nil {
											setStatus("rm rule: invalid ID: " + err.Error())
											return
										}
										go func() {
											if err := rpcClient.HVRemoveRoutingRule(v.PK, routing.RouteID(n)); err != nil {
												setStatus("rm rule failed: " + err.Error())
												return
											}
											setStatus(fmt.Sprintf("rule %d removed", n))
											refresh()
										}()
									})
							})
							return
						}
						labels := make([]string, len(rgs))
						for i, rg := range rgs {
							labels[i] = fmt.Sprintf("fwd=%d csm=%d  %s..:%d → %s..:%d",
								rg.FwdRuleID, rg.ConsumeRuleID,
								rg.Desc.SrcPK.String(), rg.Desc.SrcPort,
								rg.Desc.DstPK.String(), rg.Desc.DstPort)
						}
						queue(func() {
							showListModal("Delete route group on "+v.PK.String(), labels, func(idx int) {
								rg := rgs[idx]
								showConfirmModal("Confirm",
									fmt.Sprintf("Delete fwd rule %d and consume rule %d ?", rg.FwdRuleID, rg.ConsumeRuleID),
									func() {
										go func() {
											fwdErr := rpcClient.HVRemoveRoutingRule(v.PK, rg.FwdRuleID)
											csmErr := rpcClient.HVRemoveRoutingRule(v.PK, rg.ConsumeRuleID)
											if fwdErr != nil || csmErr != nil {
												setStatus(fmt.Sprintf("rm rule: fwd=%v csm=%v", fwdErr, csmErr))
												return
											}
											setStatus(fmt.Sprintf("rules %d/%d removed", rg.FwdRuleID, rg.ConsumeRuleID))
											refresh()
										}()
									})
							})
						})
					}()
					return nil
				case "T":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showAddTransportModal(v.PK, func(remote cipher.PubKey, tpType, label string) {
						go func() {
							setStatus("Adding transport...")
							res, err := rpcClient.HVAddTransport(v.PK, remote, tpType, label, 0)
							if err != nil {
								setStatus("add transport failed: " + err.Error())
								return
							}
							setStatus(fmt.Sprintf("transport %s.. (%s) added on %s",
								res.ID.String(), tpType, v.PK.String()))
							refresh()
						}()
					})
					return nil
				case "p":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading autoconnect status...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch summary failed: " + err.Error())
							return
						}
						current := sum.PublicAutoconnect
						newVal := !current
						queue(func() {
							showConfirmModal("Toggle public_autoconnect",
								fmt.Sprintf("Currently %v on %s. Set to %v?", current, v.PK.String(), newVal),
								func() {
									go func() {
										if err := rpcClient.HVSetPublicAutoconnect(v.PK, newVal); err != nil {
											setStatus("set autoconnect failed: " + err.Error())
											return
										}
										setStatus(fmt.Sprintf("public_autoconnect=%v on %s", newVal, v.PK.String()))
										refresh()
									}()
								})
						})
					}()
					return nil
				case "c":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showChoiceModal("calculate_routes",
						fmt.Sprintf("Local route calculation on %s\n(vs route-finder service)", v.PK.String()),
						[]string{"Enable", "Disable", "Cancel"},
						func(label string) {
							if label == "Cancel" {
								return
							}
							enable := label == "Enable"
							go func() {
								if err := rpcClient.HVSetCalculateRoutes(v.PK, enable); err != nil {
									setStatus("set calculate_routes failed: " + err.Error())
									return
								}
								setStatus(fmt.Sprintf("calculate_routes=%v on %s", enable, v.PK.String()))
								refresh()
							}()
						})
					return nil
				case "R":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showConfirmModal("Reload visor",
						fmt.Sprintf("Reload visor %s without restarting?", v.PK.String()),
						func() {
							go func() {
								if err := rpcClient.HVReload(v.PK); err != nil {
									setStatus("reload failed: " + err.Error())
									return
								}
								setStatus("reload triggered on " + v.PK.String())
								refresh()
							}()
						})
					return nil
				case "D":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showConfirmModal("Shutdown visor",
						fmt.Sprintf("SHUTDOWN visor %s? It will stop responding.", v.PK.String()),
						func() {
							go func() {
								if err := rpcClient.HVShutdown(v.PK); err != nil {
									setStatus("shutdown failed: " + err.Error())
									return
								}
								setStatus("shutdown sent to " + v.PK.String())
								refresh()
							}()
						})
					return nil
				case "h":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading service health...")
						entries, err := rpcClient.HVServiceHealth(v.PK)
						if err != nil {
							setStatus("service health failed: " + err.Error())
							return
						}
						var sb strings.Builder
						sb.WriteString(fmt.Sprintf("[yellow]Service health for %s[white]\n\n", v.PK.String()))
						sb.WriteString(fmt.Sprintf("  %-22s %-9s %-7s %-12s %s\n", "SERVICE", "STATUS", "TP", "LATENCY", "VERSION"))
						sb.WriteString("  ────────────────────────────────────────────────────────────────────────\n")
						for _, e := range entries {
							color := "red"
							if strings.EqualFold(e.Status, "ok") || strings.EqualFold(e.Status, "healthy") {
								color = "green"
							}
							lat := "-"
							if e.LatencyMs > 0 {
								lat = fmt.Sprintf("%dms", e.LatencyMs)
							}
							sb.WriteString(fmt.Sprintf("  %-22s [%s]%-9s[white] %-7s %-12s %s\n",
								e.Name, color, e.Status, e.Transport, lat, e.Version))
							if e.Error != "" {
								sb.WriteString(fmt.Sprintf("    [red]%s[white]\n", e.Error))
							}
						}
						sb.WriteString("\n[gray]Press Esc to close.[white]")
						queue(func() {
							showTextModal("Services Health", sb.String(), 100, len(entries)*2+8)
						})
					}()
					return nil
				case "G":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showConfirmModal("DMSG connect-all",
						fmt.Sprintf("Trigger DMSG connect-all on %s?", v.PK.String()),
						func() {
							go func() {
								res, err := rpcClient.HVDmsgConnectAll(v.PK)
								if err != nil {
									setStatus("connect-all failed: " + err.Error())
									return
								}
								setStatus(fmt.Sprintf("connect-all on %s: total=%d already=%d new=%d failed=%d",
									v.PK.String(), res.Total, res.AlreadyConnected, res.NewlyConnected, len(res.Failed)))
								refresh()
							}()
						})
					return nil
				case "N":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showInputModal("Set DMSG sessions_count on "+v.PK.String(), "sessions_count (0 = all)", "0", func(s string) {
						n, err := strconv.Atoi(s)
						if err != nil || n < 0 {
							setStatus("sessions_count: invalid number")
							return
						}
						go func() {
							res, err := rpcClient.HVSetDmsgSessionsCount(v.PK, n)
							if err != nil {
								setStatus("set sessions_count failed: " + err.Error())
								return
							}
							setStatus(fmt.Sprintf("sessions_count=%d on %s (total=%d already=%d new=%d fail=%d)",
								n, v.PK.String(), res.Total, res.AlreadyConnected, res.NewlyConnected, len(res.Failed)))
							refresh()
						}()
					})
					return nil
				case "l":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading apps...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch apps failed: " + err.Error())
							return
						}
						apps := sum.Overview.Apps
						labels := make([]string, len(apps))
						for i, a := range apps {
							labels[i] = a.Name
						}
						queue(func() {
							showListModal("App logs on "+v.PK.String(), labels, func(idx int) {
								name := apps[idx].Name
								go func() {
									setStatus("Loading logs for " + name + "...")
									since := time.Now().Add(-1 * time.Hour)
									logs, err := rpcClient.HVLogsSince(v.PK, since, name)
									if err != nil {
										setStatus("logs failed: " + err.Error())
										return
									}
									text := strings.Join(logs, "\n")
									if text == "" {
										text = "(no log entries in the last hour)"
									}
									queue(func() {
										showTextModal(fmt.Sprintf("%s logs (last 1h)", name), text, 110, 30)
									})
								}()
							})
						})
					}()
					return nil
				case "A":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading apps...")
						sum, err := fetchSummary(v.PK)
						if err != nil {
							setStatus("fetch apps failed: " + err.Error())
							return
						}
						apps := sum.Overview.Apps
						labels := make([]string, len(apps))
						for i, a := range apps {
							as := "off"
							if a.AutoStart {
								as = "on"
							}
							labels[i] = fmt.Sprintf("[autostart=%-3s] %s", as, a.Name)
						}
						queue(func() {
							showListModal("Toggle autostart on "+v.PK.String(), labels, func(idx int) {
								a := apps[idx]
								newVal := !a.AutoStart
								go func() {
									if err := rpcClient.HVSetAutoStart(v.PK, a.Name, newVal); err != nil {
										setStatus("set autostart failed: " + err.Error())
										return
									}
									setStatus(fmt.Sprintf("%s autostart=%v on %s", a.Name, newVal, v.PK.String()))
									refresh()
								}()
							})
						})
					}()
					return nil
				case "P":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading proxies status...")
						st, err := rpcClient.HVEmbeddedProxies(v.PK)
						if err != nil {
							setStatus("embedded proxies failed: " + err.Error())
							return
						}
						type item struct {
							kind string
							info *visorapi.EmbeddedProxyInfo
						}
						items := []item{
							{"dmsg", st.DmsgWeb},
							{"skynet", st.SkynetWeb},
						}
						labels := make([]string, 0, len(items))
						active := make([]item, 0, len(items))
						for _, it := range items {
							if it.info == nil {
								continue
							}
							active = append(active, it)
							state := "disabled"
							if it.info.Enabled {
								state = "enabled"
								if !it.info.Running {
									state = "starting"
								}
							}
							labels = append(labels, fmt.Sprintf("%-7s  %-9s  socks=%-22s  upstream=%s",
								it.kind, state, dash(it.info.SocksAddr), dash(it.info.UpstreamSOCKS)))
						}
						labels = append(labels, "[set upstream]", "[close]")
						queue(func() {
							showListModal("Resolving proxies on "+v.PK.String(), labels, func(idx int) {
								if idx >= len(active) {
									if labels[idx] == "[set upstream]" {
										queue(func() {
											showInputModal("Set upstream for proxy", "kind=dmsg|skynet  addr=host:port  (e.g. 'dmsg 127.0.0.1:9090')", "", func(s string) {
												parts := strings.Fields(s)
												if len(parts) != 2 {
													setStatus("upstream: expected 'kind addr'")
													return
												}
												kind, addr := parts[0], parts[1]
												go func() {
													if err := rpcClient.HVSetEmbeddedProxyUpstream(v.PK, kind, addr); err != nil {
														setStatus("set upstream failed: " + err.Error())
														return
													}
													setStatus(fmt.Sprintf("%s upstream=%s set", kind, addr))
												}()
											})
										})
									}
									return
								}
								it := active[idx]
								newVal := !it.info.Enabled
								go func() {
									if err := rpcClient.HVSetEmbeddedProxyEnabled(v.PK, it.kind, newVal); err != nil {
										setStatus("set proxy enabled failed: " + err.Error())
										return
									}
									setStatus(fmt.Sprintf("%s proxy enabled=%v", it.kind, newVal))
									refresh()
								}()
							})
						})
					}()
					return nil
				case "f":
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					go func() {
						setStatus("Loading ports...")
						tcpPorts, tcpErr := rpcClient.HVListTCPPorts(v.PK)
						fwdPorts, fwdErr := rpcClient.HVListForwardedPorts(v.PK)
						if tcpErr != nil && fwdErr != nil {
							setStatus(fmt.Sprintf("ports failed: tcp=%v fwd=%v", tcpErr, fwdErr))
							return
						}
						labels := []string{}
						for _, p := range tcpPorts {
							labels = append(labels, fmt.Sprintf("[tcp]  %d", p))
						}
						for _, fp := range fwdPorts {
							marks := ""
							if fp.Skynet {
								marks += "S"
							}
							if fp.DMSG {
								marks += "D"
							}
							if fp.ShowOnLanding {
								marks += "L"
							}
							if marks == "" {
								marks = "-"
							}
							labels = append(labels, fmt.Sprintf("[fwd:%s]  %-5d → %-5d  %s  %s",
								marks, fp.Port, fp.EffectiveLocalPort(), fp.Label, fp.Description))
						}
						labels = append(labels,
							"[register skynet TCP port]",
							"[deregister skynet TCP port]",
							"[register forwarded port]",
							"[close]",
						)
						queue(func() {
							showListModal("Ports on "+v.PK.String(), labels, func(idx int) {
								n := len(tcpPorts) + len(fwdPorts)
								if idx < n {
									return // selecting a row is read-only for now
								}
								switch labels[idx] {
								case "[register skynet TCP port]":
									showInputModal("Register skynet TCP port", "port number", "", func(s string) {
										p, err := strconv.Atoi(s)
										if err != nil || p <= 0 || p > 65535 {
											setStatus("invalid port")
											return
										}
										go func() {
											if err := rpcClient.HVRegisterTCPPort(v.PK, p); err != nil {
												setStatus("register tcp port failed: " + err.Error())
												return
											}
											setStatus(fmt.Sprintf("registered tcp port %d", p))
											refresh()
										}()
									})
								case "[deregister skynet TCP port]":
									showInputModal("Deregister skynet TCP port", "port number", "", func(s string) {
										p, err := strconv.Atoi(s)
										if err != nil || p <= 0 || p > 65535 {
											setStatus("invalid port")
											return
										}
										go func() {
											if err := rpcClient.HVDeregisterTCPPort(v.PK, p); err != nil {
												setStatus("deregister tcp port failed: " + err.Error())
												return
											}
											setStatus(fmt.Sprintf("deregistered tcp port %d", p))
											refresh()
										}()
									})
								case "[register forwarded port]":
									showRegisterForwardedPortModal(v.PK)
								}
							})
						})
					}()
					return nil
				}
			}
			return event
		}

		// Handle Ctrl+C and SIGTERM
		sigC := make(chan os.Signal, 1)
		signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigC)
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-sigC:
				queue(func() { quit = true })
			case <-done:
			}
		}()

		// Initial load + auto-refresh
		refresh()
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					refresh()
				}
			}
		}()

		app.Run(func(f *progkit.Frame) {
			drain()
			tableR, rest := f.Size().SplitTop(max(f.H/2, 3))
			detailR, foot := rest.SplitBottom(1)
			tableStyle, detailStyle := hvFocusStyle, hvBorderStyle
			if detailFocus {
				tableStyle, detailStyle = hvBorderStyle, hvFocusStyle
			}
			drawTable(f, progkit.Box(f.Screen, tableR, "Hypervisor — Connected Visors", tableStyle), cur == nil)
			inner := progkit.Box(f.Screen, detailR, "Visor Detail", detailStyle)
			if cur == nil {
				detail.Draw(f, inner)
			} else {
				// Without placements, so nothing in the page covers the modal.
				for i, l := range detail.Lines()[min(detail.Top(), len(detail.Lines())):] {
					if i >= inner.H {
						break
					}
					progkit.DrawLine(f.Screen, inner.X, inner.Y+i, inner.W, l)
				}
			}
			x := progkit.DrawText(f.Screen, 0, foot.Y, foot.W, " "+status, hvHeadStyle)
			progkit.DrawText(f.Screen, x, foot.Y, foot.W-x, " | "+hvKeys, tcell.StyleDefault)
			if cur != nil {
				cur.render(f)
			}
		}, func(ev tcell.Event) bool {
			drain()
			k, ok := ev.(*tcell.EventKey)
			if !ok || quit {
				return !quit
			}
			switch {
			case progkit.IsCtrl(k, 'c'):
				quit = true
			case cur != nil:
				if k.Key() == tcell.KeyEscape {
					closeModal()
				} else {
					cur.key(k)
				}
			case detailFocus && k.Key() != tcell.KeyEscape && detail.Key(k):
			case !detailFocus && k.Key() == tcell.KeyEnter:
				openDetail()
			case !detailFocus && k.Key() == tcell.KeyUp:
				sel--
			case !detailFocus && k.Key() == tcell.KeyDown:
				sel++
			case !detailFocus && k.Key() == tcell.KeyPgUp:
				sel -= 10
			case !detailFocus && k.Key() == tcell.KeyPgDn:
				sel += 10
			case !detailFocus && k.Key() == tcell.KeyHome:
				sel = 0
			case !detailFocus && k.Key() == tcell.KeyEnd:
				sel = len(visors) - 1
			default:
				onKey(k)
			}
			return !quit
		})
	},
}

// hvModal is the one dialog shown over the screen, centered.
type hvModal struct {
	title string
	w, h  int
	draw  func(f *progkit.Frame, r progkit.Rect)
	key   func(ev *tcell.EventKey)
}

func (m *hvModal) render(f *progkit.Frame) {
	w, h := min(m.w+2, f.W), min(m.h+2, f.H)
	r := progkit.Rect{X: (f.W - w) / 2, Y: (f.H - h) / 2, W: w, H: h}
	progkit.Fill(f.Screen, r, tcell.StyleDefault)
	m.draw(f, progkit.Box(f.Screen, r, m.title, tcell.StyleDefault))
}

// hvField is one row of a form: text when input is set, a choice when
// choices is, and a checkbox otherwise.
type hvField struct {
	label   string
	input   *progkit.Input
	on      bool
	choices []string
	choice  int
}

func textField(label, value string) *hvField {
	in := &progkit.Input{}
	in.SetValue(value)
	return &hvField{label: label, input: in}
}

// hvColors maps the [color] tags the detail texts are written with to SGR.
var hvColors = map[string]string{
	"yellow": "\x1b[33m", "red": "\x1b[31m", "green": "\x1b[32m",
	"cyan": "\x1b[36m", "gray": "\x1b[90m", "white": "\x1b[39m",
}

func markup(s string) string {
	for name, sgr := range hvColors {
		s = strings.ReplaceAll(s, "["+name+"]", sgr)
	}
	return s
}

func wrapText(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			if line != "" && len(line)+1+len(word) > w {
				out = append(out, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return out
}

func truncStr(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// dash stands in for an empty value.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
