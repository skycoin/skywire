// Package main cmd/skywire-cli/commands/rewards-ui/ui/ui.go
package main

import (
	"embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"cogentcore.org/core/base/mergefs"
	"cogentcore.org/core/core"
	"cogentcore.org/core/events"
	"cogentcore.org/core/htmlcore"
	"cogentcore.org/core/icons"
	"cogentcore.org/core/paint"
	"cogentcore.org/core/styles"
	"cogentcore.org/core/tree"
	"github.com/0magnet/calvin"

	"github.com/skycoin/skywire"
)

//go:embed mononoki/*.ttf
var mononoki embed.FS

type reward struct {
	Date  string  `json:"date"`
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

func main() {
	core.TheApp.SetSceneInit(func(sc *core.Scene) {
		sc.SetWidgetInit(func(w core.Widget) {
			w.AsWidget().Styler(func(s *styles.Style) {
				s.Font.Family = "mononoki"
				s.Text.LineHeight.Em(1)
				s.Text.WhiteSpace = styles.WhiteSpacePreWrap
			})
		})
	})

	b := core.NewBody("Skywire Rewards")
	paint.FontLibrary.FontsFS = mergefs.Merge(paint.FontLibrary.FontsFS, mononoki)
	paint.FontLibrary.UpdateFontsAvail()

	ts := core.NewTabs(b).SetType(core.NavigationAuto)
	first, tb := ts.NewTab("Rules")
	tb.SetIcon(icons.Home)
	core.NewText(first).SetText(calvin.AsciiFont("skywire rewards"))
	ctx := htmlcore.NewContext()
	err := htmlcore.ReadMDString(ctx, first, skywire.MainnetRules)
	if err != nil {
		log.Fatalf("Error reading embedded mainnet rules with htmlcore.ReadMDString: %v", err)
	}

	second, tb := ts.NewTab("Rewards")
	tb.SetIcon(icons.History)

	if runtime.GOOS == "js" {
		core.NewTable(second).SetSlice(func() *[]reward {
			resp, err := http.Get("/skycoin-rewards.json")
			if err != nil {
				log.Fatalf("Error fetching data: %v", err)
			}
			defer resp.Body.Close() //nolint

			if err := json.NewDecoder(resp.Body).Decode(&rewards); err != nil {
				log.Fatalf("Error decoding JSON: %v", err)
			}
			return &rewards
		}()).SetReadOnly(true)
	}
	third, tb := ts.NewTab("Reward data")
	tb.SetIcon(icons.History)
	pg := core.NewPages(third)
	pg.AddPage("home", func(pg *core.Pages) {
		for i := range rewards {
			core.NewButton(pg).SetText(rewards[i].Date).OnClick(func(_ events.Event) {
				pg.Open(rewards[i].Date + "-home")
			})
		}
	})
	for i := range rewards {
		pg.AddPage(rewards[i].Date+"-home", func(pg *core.Pages) {
			core.NewButton(pg).SetText("back").OnClick(func(_ events.Event) {
				pg.Open("home")
			})
			ts := core.NewTabs(pg).SetType(core.NavigationAuto)
			first, tb := ts.NewTab("Stats")
			tb.SetIcon(icons.Home)
			core.NewText(first).SetText("<br>Statistics<br>")

			if runtime.GOOS == "js" {
				resp, err := http.Get("/skycoin-rewards/hist/" + rewards[i].Date + "_stats.txt")
				if err != nil {
					log.Fatalf("Error fetching data: %v", err)
				}
				defer resp.Body.Close() //nolint
				bodybytes, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Fatal(err)
				}
				core.NewText(first).SetText(string(bodybytes))
			}

			second, tb := ts.NewTab("Distribution")
			tb.SetIcon(icons.Home)
			core.NewText(second).SetText("<br>Distribution Data<br>")

			if runtime.GOOS == "js" {
				resp, err := http.Get("/skycoin-rewards/hist/" + rewards[i].Date + "_rewardtxn0.csv")
				if err != nil {
					log.Fatalf("Error fetching data: %v", err)
				}
				defer resp.Body.Close() //nolint
				bodybytes, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Fatal(err)
				}
				core.NewText(second).SetText(string(bodybytes))
			}

			third, tb := ts.NewTab("Reward Shares")
			tb.SetIcon(icons.Home)
			core.NewText(third).SetText("<br>Reward Shares<br>")

			if runtime.GOOS == "js" {
				resp, err := http.Get("/skycoin-rewards/hist/" + rewards[i].Date + "_shares.csv")
				if err != nil {
					log.Fatalf("Error fetching data: %v", err)
				}
				defer resp.Body.Close() //nolint
				bodybytes, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Fatal(err)
				}
				core.NewText(third).SetText(string(bodybytes))
			}
			fourth, tb := ts.NewTab("Ineligible")
			tb.SetIcon(icons.Home)
			core.NewText(fourth).SetText("<br>Ineligible<br>")

			if runtime.GOOS == "js" {
				resp, err := http.Get("/skycoin-rewards/hist/" + rewards[i].Date + "_ineligible.csv")
				if err != nil {
					log.Fatalf("Error fetching data: %v", err)
				}
				defer resp.Body.Close() //nolint
				bodybytes, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Fatal(err)
				}
				core.NewText(fourth).SetText(string(bodybytes))
			}
			core.NewButton(pg).SetText("back").OnClick(func(_ events.Event) {
				pg.Open("home")
			})
		})
	}

	//	ct := content.NewContent(b)
	//	ctx = ct.Context

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
		core.NewTable(fifth).SetSlice(&nodes.Nodes).SetReadOnly(true)

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
