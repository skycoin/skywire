// Package clivisor hvtui.go — hypervisor TUI
package clivisor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
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

		// --- Status bar ---
		status := tview.NewTextView().
			SetDynamicColors(true).
			SetTextAlign(tview.AlignLeft)

		// --- Layout ---
		// Top: table + detail side by side
		// Bottom: status bar
		split := tview.NewFlex().
			AddItem(table, 0, 3, true).
			AddItem(detail, 0, 2, false)

		layout := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(split, 0, 1, true).
			AddItem(status, 1, 0, false)

		// --- State ---
		var (
			visors []visor.HVVisorEntry
			mu     sync.RWMutex
		)

		setStatus := func(msg string) {
			app.QueueUpdateDraw(func() {
				status.SetText(fmt.Sprintf(" [yellow]%s[white] | r:refresh  q:quit  enter:detail  esc:back", msg))
			})
		}

		// --- Populate table ---
		updateTable := func(entries []visor.HVVisorEntry) {
			table.Clear()
			// Header
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
				if len(pk) > 12 {
					pk = pk[:10] + ".."
				}
				statusStr := "[green]ok"
				if e.IsLocal {
					statusStr = "[cyan]local"
				}
				if e.Error != "" {
					statusStr = "[red]" + truncate(e.Error, 20)
				}
				ver := e.Version
				if ver == "" {
					ver = "-"
				}
				uptime := "-"
				if e.Uptime > 0 {
					uptime = (time.Duration(e.Uptime) * time.Second).Truncate(time.Second).String()
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
				table.SetCell(row, 3, tview.NewTableCell(uptime))
				table.SetCell(row, 4, tview.NewTableCell(fmt.Sprintf("%d", e.Transports)))
				table.SetCell(row, 5, tview.NewTableCell(fmt.Sprintf("%d", e.Apps)))
				table.SetCell(row, 6, tview.NewTableCell(ip))
				table.SetCell(row, 7, tview.NewTableCell(cc))
				table.SetCell(row, 8, tview.NewTableCell(statusStr))
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

			// Try to get full summary for transport/app details
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
							remote := tp.Remote.String()
							if len(remote) > 10 {
								remote = remote[:8] + ".."
							}
							sb.WriteString(fmt.Sprintf("  %s  %s  → %s  %s\n",
								tpID, strings.ToUpper(string(tp.Type)), remote, tp.Label))
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
				}
			}

			detail.SetText(sb.String())
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
					setStatus(fmt.Sprintf("%d visors | refreshed %s", len(entries), time.Now().Format("15:04:05")))
				})
			}()
		}

		// --- Selection handler ---
		table.SetSelectedFunc(func(row, _ int) {
			mu.RLock()
			defer mu.RUnlock()
			idx := row - 1 // skip header
			if idx < 0 || idx >= len(visors) {
				return
			}
			go func() {
				setStatus("Loading detail...")
				showDetail(visors[idx])
				setStatus(fmt.Sprintf("%d visors | detail view", len(visors)))
				app.QueueUpdateDraw(func() {
					app.SetFocus(detail)
				})
			}()
		})

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
				case 'r', 'R':
					refresh()
					return nil
				}
			}
			return event
		})

		// --- Auto-refresh ---
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				refresh()
			}
		}()

		// Initial load
		refresh()

		if err := app.SetRoot(layout, true).EnableMouse(true).Run(); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
	},
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
