// Package main cmd/skywire-cli/commands/rewards-ui/ui/ui.go
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"cogentcore.org/core/core"
	"cogentcore.org/core/events"
	"cogentcore.org/core/htmlcore"
	"cogentcore.org/core/icons"
	"cogentcore.org/core/styles"
	"cogentcore.org/core/text/fonts"
	"cogentcore.org/core/text/rich"
	"cogentcore.org/core/text/text"
	"cogentcore.org/core/tree"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
)

//go:embed mononoki/*.ttf
var mononoki embed.FS

type reward struct {
	Date  Date    `json:"date"`
	Pool1 float64 `json:"1"`
	Pool2 float64 `json:"2"`
	Sent  string  `json:"sent"`
}

var rewards []reward

type node struct {
	PK        string `json:"pk"`
	Time      string `json:"time"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	StartedAt string `json:"started_at"`
}

type nodesResponse struct {
	Nodes []node `json:"nodes"`
}

var nodes nodesResponse

type nodeMod struct {
	PK        string `json:"pk"`
	Age       string `json:"age"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	StartedAt string `json:"started_at"`
}

type nodesModResponse struct {
	Nodes []nodeMod `json:"nodes"`
}

var nodesMod nodesModResponse

var pg *core.Pages
var currentEntry int

func init() {
	core.AddValueType[Date, DateButton]()
}

// Date is used to wrap the date in a table with a button
type Date string

// DateButton is used to wrap the date in a table with a button
type DateButton struct {
	core.Button
}

// WidgetValue is used to wrap the date in a table with a button
func (db *DateButton) WidgetValue() any { return &db.Text }

// Init is used to wrap the date in a table with a button
func (db *DateButton) Init() {
	db.Button.Init()
	db.SetType(core.ButtonTonal)
	db.SetTooltip("Click to view details")
	db.OnClick(func(_ events.Event) {
		currentEntry = db.Property(core.ListRowProperty).(int)
		pg.Open("details")
	})
}

func main() {
	fonts.AddEmbedded(mononoki)
	core.TheApp.SetSceneInit(func(sc *core.Scene) {
		sc.SetWidgetInit(func(w core.Widget) {
			w.AsWidget().Styler(func(s *styles.Style) {
				s.Font.Family = rich.Monospace
				s.Font.CustomFont = "mononoki"
				s.Text.LineHeight = 1
				s.Text.WhiteSpace = text.WhiteSpacePreWrap
			})
		})
	})

	b := core.NewBody("Skywire Rewards")

	ts := core.NewTabs(b).SetType(core.NavigationAuto)
	zeroeth, tb := ts.NewTab("Rules")
	tb.SetIcon(icons.Home)
	core.NewText(zeroeth).SetText(calvin.AsciiFont("skywire rewards"))
	ctx := htmlcore.NewContext()
	err := htmlcore.ReadMDString(ctx, zeroeth, skywire.MainnetRules)
	if err != nil {
		log.Fatalf("Error reading embedded mainnet rules with htmlcore.ReadMDString: %v", err)
	}
	first, tb := ts.NewTab("Stats")
	tb.SetIcon(icons.Home)
	if runtime.GOOS == "js" {
		ts := core.NewTabs(first).SetType(core.NavigationAuto)
		versiontab, _ := ts.NewTab("Version")
		core.NewText(versiontab).SetText(httpGetString("/stats/ut"))
		archtab, _ := ts.NewTab("Architectures")
		core.NewText(archtab).SetText(httpGetString("/stats/arch"))
		ostab, _ := ts.NewTab("OS")
		core.NewText(ostab).SetText(httpGetString("/stats/os"))
		cputab, _ := ts.NewTab("CPU")
		core.NewText(cputab).SetText(httpGetString("/stats/cpu"))
		memtab, _ := ts.NewTab("Memory")
		core.NewText(memtab).SetText(httpGetString("/stats/mem"))
		ramtab, _ := ts.NewTab("RAM")
		core.NewText(ramtab).SetText(httpGetString("/stats/ram"))
	}

	second, tb := ts.NewTab("Rewards")
	tb.SetIcon(icons.History)

	if runtime.GOOS == "js" {
		pg = core.NewPages(second)
		pg.AddPage("Rewards", func(pg *core.Pages) {
			tb := core.NewTable(pg).SetSlice(func() *[]reward {
				resp, err := http.Get("/skycoin-rewards.json")
				if err != nil {
					log.Fatalf("Error fetching data: %v", err)
				}
				defer resp.Body.Close() //nolint

				if err := json.NewDecoder(resp.Body).Decode(&rewards); err != nil {
					log.Fatalf("Error decoding JSON: %v", err)
				}
				return &rewards
			}())
			tb.SetReadOnly(true)
		})
		pg.AddPage("details", func(pg *core.Pages) {
			core.NewButton(pg).SetText("back").OnClick(func(_ events.Event) {
				pg.Open("Rewards")
			})
			r := rewards[currentEntry]
			core.NewText(pg).SetType(core.TextHeadlineSmall).SetText("Reward Distribution Details for " + string(r.Date))
			txid := strings.Replace(httpGetString("/skycoin-rewards/hist/"+string(r.Date)+".txt"), "\n", "", -1)
			if txid != "" {
				core.NewText(pg).SetText(`TXID: ` + txid)
				htmlcore.ReadHTMLString(ctx, pg, `<a href="https://explorer.skycoin.com/app/transaction/`+txid+`">`+txid+`</a>`) //nolint
			} else {
				core.NewText(pg).SetText(`Rewards not yet distributed`)
			}

			ts := core.NewTabs(pg).SetType(core.NavigationAuto)
			first, tb := ts.NewTab("Stats")
			tb.SetIcon(icons.Home)
			core.NewText(first).SetText("<br>Statistics<br>")
			core.NewText(first).SetText(httpGetString("/skycoin-rewards/hist/" + string(r.Date) + "_stats.txt"))
			second, tb := ts.NewTab("Distribution")
			tb.SetIcon(icons.Home)
			core.NewText(second).SetText("<br>Distribution Data<br>")
			core.NewText(second).SetText(httpGetString("/skycoin-rewards/hist/" + string(r.Date) + "_rewardtxn0.csv"))
			third, tb := ts.NewTab("Reward Shares")
			tb.SetIcon(icons.Home)
			core.NewText(third).SetText("<br>Reward Shares<br>")
			core.NewText(third).SetText(httpGetString("/skycoin-rewards/hist/" + string(r.Date) + "_shares.csv"))
			fourth, tb := ts.NewTab("Ineligible")
			tb.SetIcon(icons.Home)
			core.NewText(fourth).SetText("<br>Ineligible<br>")
			core.NewText(fourth).SetText(httpGetString("/skycoin-rewards/hist/" + string(r.Date) + "_ineligible.csv"))
			core.NewButton(pg).SetText("back").OnClick(func(_ events.Event) {
				pg.Open("Rewards")
			})
		})

		back := core.NewButton(pg).SetType(core.ButtonTonal).SetText("Back").SetIcon(icons.ArrowBack)
		back.OnClick(func(_ events.Event) {
			pg.Open("Rewards")
		})
	}

	fourth, tb := ts.NewTab("Log Collection")
	tb.SetIcon(icons.History)
	htmlcore.ReadHTMLString(ctx, fourth, "<a href='/log-collection'>Log Collection</a>") //nolint
	fifth, tb := ts.NewTab("Survey Index")
	tb.SetIcon(icons.History)
	if runtime.GOOS == "js" {

		resp, err := http.Get("/log-collection/json")
		if err != nil {
			log.Fatalf("Error fetching data: %v", err)
		}
		defer resp.Body.Close() //nolint

		if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
			log.Fatalf("Error decoding JSON: %v", err)
		}

		nodesMod.Nodes = make([]nodeMod, 0, len(nodes.Nodes))

		for _, n := range nodes.Nodes {
			parsedTime, err := time.Parse(time.RFC3339, n.Time)
			if err != nil {
				log.Printf("Error parsing time for node %s: %v", n.PK, err)
				continue // or set Age to "unknown"
			}
			duration := time.Since(parsedTime)

			// Format duration to something human readable, like "1h2m"
			age := fmtDuration(duration)

			nodesMod.Nodes = append(nodesMod.Nodes, nodeMod{
				PK:        n.PK,
				Age:       age,
				Version:   n.Version,
				Commit:    n.Commit,
				Date:      n.Date,
				StartedAt: n.StartedAt,
			})
		}
		core.NewTable(fifth).SetSlice(&nodesMod.Nodes).SetReadOnly(true)

	}
	b.AddTopBar(func(bar *core.Frame) {
		tb := core.NewToolbar(bar)
		//		tb.Maker(ct.MakeToolbar)
		tb.Maker(func(p *tree.Plan) {
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, https(skywire.Prod.UptimeTracker)+"/uptimes?v=v2")
				w.SetText("Uptime").SetIcon(icons.OnlinePrediction)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, https(skywire.Prod.AddressResolver)+"/transports")
				w.SetText("Address Resolver").SetIcon(icons.SatelliteAltFill)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, https(skywire.Prod.TransportDiscovery)+"/all-transports")
				w.SetText("Transport-Discovery").SetIcon(icons.Search)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, https(skywire.Prod.DmsgDiscovery)+"/dmsg-discovery/entries")
				w.SetText("Dmsg-Discovery").SetIcon(icons.Search)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, https(skywire.Prod.DmsgDiscovery)+"/dmsg-discovery/all_servers")
				w.SetText("Dmsg Servers").SetIcon(icons.TrafficFill)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, https(skywire.Prod.DmsgDiscovery)+"/dmsg-discovery/available_servers")
				w.SetText("Dmsg Servers").SetIcon(icons.Traffic)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, "https://t.me/skywire")
				w.SetText("@skywire").SetIcon(icons.Support)
			})
			tree.Add(p, func(w *core.Button) {
				ctx.LinkButton(w, "https://t.me/skywire_reward")
				w.SetText("@skywire_reward").SetIcon(icons.NotificationImportant)
			})
		})
	})

	go func() {
		for range time.NewTicker(1200 * time.Second).C {
			b.Update()
		}
	}()
	b.RunMainWindow()
}

func https(a string) string {
	return strings.ReplaceAll(a, "http://", "https://")
}

func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

func httpGetString(url string) string {
	resp, err := http.Get(url) //nolint
	if err == nil {
		defer resp.Body.Close() //nolint
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		return string(bodyBytes)
	}
	return ""
}

/*
	l := fmt.Sprintf("There are %d days in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day(), time.Now().Month())
	l += fmt.Sprintf("Today is %s %d.\n", time.Now().Month(), time.Now().Day())
	l += fmt.Sprintf("There are %d days left in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()-time.Now().Day(), time.Now().Month())
	l += fmt.Sprintf("%d days in the year %d.\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay(), time.Now().Year())
	l += fmt.Sprintf("Today is day %d.\n", time.Now().YearDay())
	l += fmt.Sprintf("There are %d days remaining in %d<br>", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay()-time.Now().YearDay(), time.Now().Year())
	l += "\n" + string(ansihtml.ConvertToHTML([]byte(cal())))
	htmlcore.ReadHTMLString(htmlcore.NewContext(), second, l)
*/
