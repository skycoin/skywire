// Package clivisor hvtui.go — hypervisor TUI
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

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	hvCmd.AddCommand(hvTUICmd)
}

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

		app := tview.NewApplication()

		// --- Main visor list table ---
		table := tview.NewTable().
			SetBorders(false).
			SetSelectable(true, false).
			SetFixed(1, 0)
		table.SetBorder(true).
			SetTitle(" Hypervisor — Connected Visors ").
			SetTitleAlign(tview.AlignLeft)

		// --- Detail panel ---
		detail := tview.NewTextView().
			SetDynamicColors(true).
			SetScrollable(true)
		detail.SetBorder(true).
			SetTitle(" Visor Detail ").
			SetTitleAlign(tview.AlignLeft)
		detail.SetText("[gray]Select a visor to view details")

		// --- Status bar ---
		statusBar := tview.NewTextView().
			SetDynamicColors(true).
			SetTextAlign(tview.AlignLeft).
			SetText(" [yellow]Loading...[white] | q:quit r:refresh enter:detail esc:back  m:min-hops M:mux c:calc-rt w:reward p:autoconn  s:app S:stop  T:add-tp t:rm-tp  x:rm-rule  R:reload D:shutdown")

		// --- Layout ---
		split := tview.NewFlex().
			AddItem(table, 0, 3, true).
			AddItem(detail, 0, 2, false)
		layout := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(split, 0, 1, true).
			AddItem(statusBar, 1, 0, false)

		// --- State ---
		var (
			visors []visor.HVVisorEntry
			mu     sync.RWMutex
		)

		setStatus := func(msg string) {
			app.QueueUpdateDraw(func() {
				statusBar.SetText(fmt.Sprintf(" [yellow]%s[white] | q:quit r:refresh enter:detail esc:back  m:min-hops M:mux c:calc-rt w:reward p:autoconn  s:app S:stop  T:add-tp t:rm-tp  x:rm-rule  R:reload D:shutdown", msg))
			})
		}

		// --- Modal helpers ---
		showModal := func(p tview.Primitive, width, height int) {
			wrap := tview.NewFlex().
				AddItem(nil, 0, 1, false).
				AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
					AddItem(nil, 0, 1, false).
					AddItem(p, height, 1, true).
					AddItem(nil, 0, 1, false), width, 1, true).
				AddItem(nil, 0, 1, false)
			app.SetRoot(wrap, true).SetFocus(p)
		}
		closeModal := func() {
			app.SetRoot(layout, true).SetFocus(table)
		}
		showInputModal := func(title, label, defaultValue string, onSubmit func(value string)) {
			form := tview.NewForm().
				AddInputField(label, defaultValue, 64, nil, nil)
			form.AddButton("OK", func() {
				v := form.GetFormItem(0).(*tview.InputField).GetText()
				closeModal()
				onSubmit(strings.TrimSpace(v))
			})
			form.AddButton("Cancel", closeModal)
			form.SetBorder(true).SetTitle(" " + title + " ").SetTitleAlign(tview.AlignLeft)
			showModal(form, 80, 7)
		}
		showConfirmModal := func(title, message string, onConfirm func()) {
			m := tview.NewModal().
				SetText(message).
				AddButtons([]string{"Yes", "No"}).
				SetDoneFunc(func(_ int, label string) {
					closeModal()
					if label == "Yes" {
						onConfirm()
					}
				})
			m.SetBorder(true).SetTitle(" " + title + " ")
			app.SetRoot(m, true).SetFocus(m)
		}
		showListModal := func(title string, items []string, onSelect func(idx int)) {
			if len(items) == 0 {
				showConfirmModal(title, "No items available.", func() {})
				return
			}
			list := tview.NewList().ShowSecondaryText(false)
			for i, label := range items {
				idx := i
				list.AddItem(label, "", 0, func() {
					closeModal()
					onSelect(idx)
				})
			}
			list.SetBorder(true).SetTitle(" " + title + " ").SetTitleAlign(tview.AlignLeft)
			list.SetDoneFunc(closeModal)
			height := len(items) + 4
			if height > 24 {
				height = 24
			}
			showModal(list, 80, height)
		}
		fetchSummary := func(pk cipher.PubKey) (*visor.Summary, error) {
			return rpcClient.HVVisorSummary(pk)
		}
		showAddTransportModal := func(targetPK cipher.PubKey, onSubmit func(remote cipher.PubKey, tpType, label string)) {
			form := tview.NewForm()
			form.AddInputField("remote PK", "", 64, nil, nil).
				AddDropDown("type", []string{"stcpr", "sudph", "stcp", "dmsg"}, 0, nil).
				AddInputField("label", "user", 32, nil, nil)
			form.AddButton("OK", func() {
				pkStr := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
				_, tpType := form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
				label := strings.TrimSpace(form.GetFormItem(2).(*tview.InputField).GetText())
				closeModal()
				if pkStr == "" {
					setStatus("add transport: remote PK required")
					return
				}
				var rpk cipher.PubKey
				if err := rpk.Set(pkStr); err != nil {
					setStatus("add transport: invalid PK: " + err.Error())
					return
				}
				onSubmit(rpk, tpType, label)
			})
			form.AddButton("Cancel", closeModal)
			form.SetBorder(true).
				SetTitle(" Add transport on " + targetPK.String()[:8] + " ").
				SetTitleAlign(tview.AlignLeft)
			showModal(form, 80, 11)
		}

		// --- Populate table ---
		updateTable := func(entries []visor.HVVisorEntry) {
			table.Clear()
			headers := []string{"#", "PK", "VERSION", "UPTIME", "TP", "APPS", "IP", "CC", "STATUS"}
			for i, h := range headers {
				cell := tview.NewTableCell(h).
					SetTextColor(tcell.ColorYellow).
					SetSelectable(false).
					SetExpansion(1)
				if i == 1 {
					cell.SetExpansion(3)
				}
				table.SetCell(0, i, cell)
			}
			for i, e := range entries {
				row := i + 1
				pk := e.PK.String()
				st := "ok"
				stColor := tcell.ColorGreen
				if e.IsLocal {
					st = "local"
					stColor = tcell.ColorAqua
				}
				if e.Error != "" {
					st = truncStr(e.Error, 20)
					stColor = tcell.ColorRed
				}
				ver := e.Version
				if ver == "" {
					ver = "-"
				}
				up := "-"
				if e.Uptime > 0 {
					up = (time.Duration(e.Uptime) * time.Second).Truncate(time.Second).String()
				}
				ip := e.PublicIP
				if ip == "" {
					ip = "-"
				}
				cc := e.CountryCode
				if cc == "" {
					cc = "-"
				}
				table.SetCell(row, 0, tview.NewTableCell(fmt.Sprintf("%d", row)).SetTextColor(tcell.ColorDarkGray))
				table.SetCell(row, 1, tview.NewTableCell(pk))
				table.SetCell(row, 2, tview.NewTableCell(ver))
				table.SetCell(row, 3, tview.NewTableCell(up))
				table.SetCell(row, 4, tview.NewTableCell(fmt.Sprintf("%d", e.Transports)))
				table.SetCell(row, 5, tview.NewTableCell(fmt.Sprintf("%d", e.Apps)))
				table.SetCell(row, 6, tview.NewTableCell(ip))
				table.SetCell(row, 7, tview.NewTableCell(cc))
				table.SetCell(row, 8, tview.NewTableCell(st).SetTextColor(stColor))
			}
		}

		// --- Show detail for selected visor ---
		showDetail := func(e visor.HVVisorEntry) {
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
				}
			}
			app.QueueUpdateDraw(func() {
				detail.SetText(sb.String())
			})
		}

		// --- Refresh data ---
		refresh := func() {
			setStatus("Refreshing...")
			go func() {
				entries, err := rpcClient.HVListVisors()
				if err != nil {
					setStatus(fmt.Sprintf("Error: %s", err))
					return
				}
				mu.Lock()
				visors = entries
				mu.Unlock()
				app.QueueUpdateDraw(func() {
					updateTable(entries)
				})
				setStatus(fmt.Sprintf("%d visors | refreshed %s", len(entries), time.Now().Format("15:04:05")))
			}()
		}

		// --- Selection handler ---
		table.SetSelectedFunc(func(row, _ int) {
			mu.RLock()
			defer mu.RUnlock()
			idx := row - 1
			if idx < 0 || idx >= len(visors) {
				return
			}
			v := visors[idx]
			go func() {
				setStatus("Loading detail...")
				showDetail(v)
				setStatus(fmt.Sprintf("%d visors | detail view", len(visors)))
				app.QueueUpdateDraw(func() {
					app.SetFocus(detail)
				})
			}()
		})

		// --- Action helpers ---
		selectedVisor := func() (visor.HVVisorEntry, bool) {
			row, _ := table.GetSelection()
			idx := row - 1
			mu.RLock()
			defer mu.RUnlock()
			if idx < 0 || idx >= len(visors) {
				return visor.HVVisorEntry{}, false
			}
			return visors[idx], true
		}

		// --- Key handlers ---
		app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			switch event.Key() {
			case tcell.KeyEscape:
				app.SetFocus(table)
				return nil
			case tcell.KeyRune:
				switch event.Rune() {
				case 'q', 'Q':
					app.Stop()
					return nil
				case 'r':
					refresh()
					return nil
				case 'm':
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showInputModal("Set min_hops on "+v.PK.String()[:8], "min_hops", "0", func(s string) {
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
							setStatus(fmt.Sprintf("set min_hops=%d on %s", n, v.PK.String()[:8]))
							refresh()
						}()
					})
					return nil
				case 'w':
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showInputModal("Set reward address on "+v.PK.String()[:8], "address", v.RewardAddress, func(s string) {
						go func() {
							if _, err := rpcClient.HVSetRewardAddress(v.PK, s); err != nil {
								setStatus("set reward failed: " + err.Error())
								return
							}
							setStatus("reward address set on " + v.PK.String()[:8])
							refresh()
						}()
					})
					return nil
				case 's':
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
						app.QueueUpdateDraw(func() {
							showListModal("Toggle app on "+v.PK.String()[:8], labels, func(idx int) {
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
									setStatus("toggled " + a.Name + " on " + v.PK.String()[:8])
									refresh()
								}()
							})
						})
					}()
					return nil
				case 'S':
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
						app.QueueUpdateDraw(func() {
							showListModal("Stop app on "+v.PK.String()[:8], labels, func(idx int) {
								name := running[idx].Name
								go func() {
									if err := rpcClient.HVStopApp(v.PK, name); err != nil {
										setStatus("stop " + name + " failed: " + err.Error())
										return
									}
									setStatus("stopped " + name + " on " + v.PK.String()[:8])
									refresh()
								}()
							})
						})
					}()
					return nil
				case 't':
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
							short := tp.ID.String()[:8] + ".."
							labels[i] = fmt.Sprintf("%s  %-6s  %s..  %s", short, strings.ToUpper(string(tp.Type)), tp.Remote.String()[:8], tp.Label)
						}
						app.QueueUpdateDraw(func() {
							showListModal("Delete transport on "+v.PK.String()[:8], labels, func(idx int) {
								tp := tps[idx]
								short := tp.ID.String()[:8] + ".."
								showConfirmModal("Confirm",
									fmt.Sprintf("Delete transport %s (%s) ?", short, tp.Type),
									func() {
										go func() {
											if err := rpcClient.HVRemoveTransport(v.PK, tp.ID); err != nil {
												setStatus("rm transport failed: " + err.Error())
												return
											}
											setStatus("transport " + short + " removed on " + v.PK.String()[:8])
											refresh()
										}()
									})
							})
						})
					}()
					return nil
				case 'x':
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
							app.QueueUpdateDraw(func() {
								showInputModal("Delete rule by ID on "+v.PK.String()[:8],
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
								rg.Desc.SrcPK.String()[:8], rg.Desc.SrcPort,
								rg.Desc.DstPK.String()[:8], rg.Desc.DstPort)
						}
						app.QueueUpdateDraw(func() {
							showListModal("Delete route group on "+v.PK.String()[:8], labels, func(idx int) {
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
				case 'T':
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
								res.ID.String()[:8], tpType, v.PK.String()[:8]))
							refresh()
						}()
					})
					return nil
				case 'p':
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
						app.QueueUpdateDraw(func() {
							showConfirmModal("Toggle public_autoconnect",
								fmt.Sprintf("Currently %v on %s. Set to %v?", current, v.PK.String()[:8], newVal),
								func() {
									go func() {
										if err := rpcClient.HVSetPublicAutoconnect(v.PK, newVal); err != nil {
											setStatus("set autoconnect failed: " + err.Error())
											return
										}
										setStatus(fmt.Sprintf("public_autoconnect=%v on %s", newVal, v.PK.String()[:8]))
										refresh()
									}()
								})
						})
					}()
					return nil
				case 'M':
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showInputModal("Set mux_routes on "+v.PK.String()[:8], "mux_routes (0 or 1 = single)", "1", func(s string) {
						n, err := strconv.Atoi(s)
						if err != nil || n < 0 {
							setStatus("mux_routes: invalid number")
							return
						}
						go func() {
							if err := rpcClient.HVSetMuxRoutes(v.PK, n); err != nil {
								setStatus("set mux_routes failed: " + err.Error())
								return
							}
							setStatus(fmt.Sprintf("mux_routes=%d on %s", n, v.PK.String()[:8]))
							refresh()
						}()
					})
					return nil
				case 'c':
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					m := tview.NewModal().
						SetText(fmt.Sprintf("Local route calculation on %s\n(vs route-finder service)", v.PK.String()[:8])).
						AddButtons([]string{"Enable", "Disable", "Cancel"}).
						SetDoneFunc(func(_ int, label string) {
							closeModal()
							if label == "Cancel" {
								return
							}
							enable := label == "Enable"
							go func() {
								if err := rpcClient.HVSetCalculateRoutes(v.PK, enable); err != nil {
									setStatus("set calculate_routes failed: " + err.Error())
									return
								}
								setStatus(fmt.Sprintf("calculate_routes=%v on %s", enable, v.PK.String()[:8]))
								refresh()
							}()
						})
					m.SetBorder(true).SetTitle(" calculate_routes ")
					app.SetRoot(m, true).SetFocus(m)
					return nil
				case 'R':
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showConfirmModal("Reload visor",
						fmt.Sprintf("Reload visor %s without restarting?", v.PK.String()[:8]),
						func() {
							go func() {
								if err := rpcClient.HVReload(v.PK); err != nil {
									setStatus("reload failed: " + err.Error())
									return
								}
								setStatus("reload triggered on " + v.PK.String()[:8])
								refresh()
							}()
						})
					return nil
				case 'D':
					v, ok := selectedVisor()
					if !ok {
						return nil
					}
					showConfirmModal("Shutdown visor",
						fmt.Sprintf("SHUTDOWN visor %s? It will stop responding.", v.PK.String()[:8]),
						func() {
							go func() {
								if err := rpcClient.HVShutdown(v.PK); err != nil {
									setStatus("shutdown failed: " + err.Error())
									return
								}
								setStatus("shutdown sent to " + v.PK.String()[:8])
								refresh()
							}()
						})
					return nil
				}
			}
			return event
		})

		// Handle Ctrl+C
		sigC := make(chan os.Signal, 1)
		signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigC
			app.Stop()
		}()

		// Initial load + auto-refresh after app starts
		go func() {
			time.Sleep(100 * time.Millisecond)
			refresh()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				refresh()
			}
		}()

		if err := app.SetRoot(layout, true).EnableMouse(true).Run(); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
	},
}

func truncStr(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
