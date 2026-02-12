// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/server.go
package clirewardsserver

import (
	"bytes"
	"context"
	"fmt"
	htmpl "html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	"github.com/gin-gonic/gin"
	"github.com/robert-nix/ansihtml"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/tpviz"
)

// TODO: fix gocyclo error.
//
//gocyclo:ignore
func server(e error) {

	log := logging.MustGetLogger("dmsghttp")
	if dmsgDisc == "" {
		log.Fatal("Dmsg Discovery URL not specified")
	}

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	defer cancel()
	pk, err := sk.PubKey()
	if err != nil {
		pk, sk = cipher.GenerateKeyPair()
	}
	if wl != "" {
		wlk := strings.Split(wl, ",")
		for _, key := range wlk {
			var pk1 cipher.PubKey
			err := pk1.Set(key)
			if err == nil {
				wlkeys = append(wlkeys, pk1)
			}
		}
	}
	if len(wlkeys) > 0 {
		if len(wlkeys) == 1 {
			log.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
		} else {
			log.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
		}
	}
	dconf := dmsg.DefaultConfig()
	dconf.MinSessions = dmsgSess
	dmsgclient := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), dconf)
	defer func() {
		if err := dmsgclient.Close(); err != nil {
			log.WithError(err).Error()
		}
	}()

	go dmsgclient.Serve(context.Background())

	select {
	case <-ctx.Done():
		log.WithError(ctx.Err()).Warn()
		return

	case <-dmsgclient.Ready():
	}

	lis, err := dmsgclient.Listen(dmsgPort) //nolint: gosec
	if err != nil {
		log.WithError(err).Fatal()
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			log.WithError(err).Error()
		}
	}()

	htmlPageTemplateData = htmlTemplateData{
		Title: "skycoin rewards",
		Page:  "front",
	}
	var err1 error
	tmpl, err1 = htmpl.New("index").Parse(htmlMainPageTemplate)
	if err1 != nil {
		fmt.Println("Error parsing index template:", err1)
	}
	_, err1 = tmpl.New("head").Parse(htmlHeadTemplate)
	if err1 != nil {
		fmt.Println("Error parsing head template:", err1)
	}

	r1 := gin.New()
	// Disable Gin's default logger middleware
	r1.Use(gin.Recovery())
	r1.Use(loggingMiddleware())

	r1.GET("/health", func(c *gin.Context) {
		runTime = time.Since(startTime)
		nextrun, _ := script.Exec(`systemctl status skywire-reward.timer --lines=0`).First(5).Last(1).Replace("    Trigger: ", "").String() //nolint:errcheck,gosec
		prevDuration, _ := script.Exec(`systemctl status skywire-reward.service --lines=0`).Match("Duration").First(1).String()             //nolint:errcheck,gosec
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String()                                                     //nolint:errcheck,gosec
		c.JSON(http.StatusOK, gin.H{
			"frontend_start_time":             startTime,
			"frontend_run_time":               runTime.String(),
			"dmsg_discovery":                  dmsgDisc,
			"dmsg_address":                    fmt.Sprintf("%s:%d", pk.String(), dmsgPort),
			"reward_system_active":            strings.TrimRight(active, "\n"),
			"reward_system_next_run":          strings.TrimRight(nextrun, "\n"),
			"reward_system_prev_run_duration": strings.TrimRight(prevDuration, "\n"),
			"version":                         buildinfo.Version(),
			"commit":                          buildinfo.Commit(),
			"build_date":                      buildinfo.Date(),
			"whitelisted_keys":                wlkeys,
		})

		// In health-only mode, skip all other routes
	})
	if !healthOnly {
		// endpoint for testing minimum response time of curl via socks5 proxy / stand-in for latency test
		// https://dev.to/tigt/making-the-worlds-fastest-website-and-other-mistakes-56na
		// This is the fastest web page. You may not like it, but this is what peak performance looks like.
		r1.GET("/204", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Status(http.StatusNoContent)
		})

		r1.GET("/transports", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Transport statistics</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(navlinks)) //nolint:errcheck,gosec
			c.Writer.Flush()
			tpstats, _ := script.Exec("skywire cli tp tree -s").Bytes() //nolint:errcheck,gosec
			c.Writer.Write(ansihtml.ConvertToHTML(tpstats))             //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(htmlend)) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		/* //consumes excessive server resources when network is heavily transported*/
		r1.GET("/transports-map", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Transport Map</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(navlinks)) //nolint:errcheck,gosec
			c.Writer.Flush()
			tpstats, _ := script.Exec("skywire cli tp tree -s").Match("Count of transports:").Replace("Count of transports: ", "").Replace("\n", "").String() //nolint:errcheck,gosec
			tpcount, _ := strconv.Atoi(tpstats)                                                                                                               //nolint:errcheck,gosec
			if tpcount < 400 {
				tpTree, _ := script.Exec("skywire cli tp tree").Bytes() //nolint:errcheck,gosec
				c.Writer.Write(ansihtml.ConvertToHTML(tpTree))          //nolint:errcheck,gosec
				c.Writer.Flush()
			} else {
				c.Writer.Write([]byte(fmt.Sprintf("Transport count: %v exceeds server resources to map", tpcount))) //nolint:errcheck,gosec,staticcheck
				c.Writer.Flush()
			}
			c.Writer.Write([]byte(htmlend)) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		// Create tpviz server with file caching (same as standalone skywire cli tp viz)
		tpvizCfg := tpviz.DefaultConfig()
		tpvizCfg.CacheFile = filepath.Join(wd, "tpd.json")
		tpvizCfg.CacheFileUT = filepath.Join(wd, "ut.json")
		tpvizCfg.CacheFileSD = filepath.Join(wd, "sd.json")
		tpvizCfg.SurveyDir = filepath.Join(wd, "log_backups") // Survey data for IP grouping
		tpvizServer := tpviz.NewServer(tpvizCfg)
		tpvizServer.Start() // Initialize cache and start auto-refresh

		// Delegate /api/* to tpviz server (uses file caching to avoid rate limits)
		// Note: /health is already registered above with system health info
		// /api/health provides tpviz cache info for auto-refresh
		tpvizHandler := tpvizServer.Handler()
		r1.GET("/api/transports", gin.WrapH(tpvizHandler))
		r1.GET("/api/uptimes", gin.WrapH(tpvizHandler))
		r1.GET("/api/services", gin.WrapH(tpvizHandler))
		r1.GET("/api/health", gin.WrapH(tpvizHandler))
		r1.GET("/api/ip-groups", gin.WrapH(tpvizHandler))
		r1.GET("/bundle.js", gin.WrapH(tpvizHandler))

		r1.GET("/transport-graph", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
			c.Writer.WriteHeader(http.StatusOK)
			// Use full embedded index.html with nav links
			navLinks := []tpviz.NavLink{
				{URL: "/", Label: "fiber"},
				{URL: "/skycoin-rewards", Label: "rewards"},
				{URL: "/stats", Label: "stats"},
			}
			html, err := tpviz.GetEmbeddedIndexWithNavLinks(navLinks)
			if err != nil {
				c.Writer.Write([]byte("Error loading transport graph: " + err.Error())) //nolint:errcheck,gosec
				return
			}
			c.Writer.Write([]byte(html)) //nolint:errcheck,gosec
		})
		r1.GET("/log-collection", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
			c.Writer.Header().Set("Transfer-Encoding", "chunked") //nolint:errcheck,gosec
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Survey and Transport Log Collection</title></head>")) //nolint:errcheck,gosec  //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte("<body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n#latest-content-anchor {\n  visibility: hidden;\n}\n</style>\n<pre>")) //nolint:errcheck,gosec  //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(navlinks)) //nolint:errcheck,gosec
			c.Writer.Flush()
			tmpFile, err := os.CreateTemp(os.TempDir(), "*.sh")
			if err != nil {
				return
			}
			if err := tmpFile.Close(); err != nil {
				return
			}
			_, _ = script.Exec(`chmod +x ` + tmpFile.Name()).String()                                         //nolint:errcheck,gosec
			_, _ = script.Echo(nextlogrun).WriteFile(tmpFile.Name())                                          //nolint:errcheck,gosec
			res, _ := script.Exec(`bash -c 'source ` + tmpFile.Name() + ` ; _nextskywireclilogrun'`).String() //nolint:errcheck,gosec
			os.Remove(tmpFile.Name())                                                                         //nolint:errcheck,gosec
			c.Writer.Write([]byte(fmt.Sprintf("%s\n", res)))                                                  //nolint:errcheck,gosec,staticcheck
			c.Writer.Flush()

			// Initial line count
			initialLineCount, _ := script.File(wd + `/` + "skywire-cli-log.txt").CountLines() //nolint:errcheck,gosec
			// Read and print the initial lines
			initialContent, _ := script.File(wd + `/` + "skywire-cli-log.txt").First(initialLineCount).Bytes() //nolint:errcheck,gosec
			c.Writer.Write(ansihtml.ConvertToHTML(initialContent))                                             //nolint:errcheck,gosec
			c.Writer.Flush()
			for {
				select {
				case <-c.Writer.CloseNotify():
					return
				default:
				}
				// Sleep for a short duration
				time.Sleep(100 * time.Millisecond)
				// Get the current line count
				currentLineCount, _ := script.File(wd + `/` + "skywire-cli-log.txt").CountLines() //nolint:errcheck,gosec
				// Check if there are new lines
				if currentLineCount > initialLineCount {
					newContent, _ := script.File(wd + `/` + "skywire-cli-log.txt").Last(currentLineCount - initialLineCount).Bytes() //nolint:errcheck,gosec
					initialLineCount = currentLineCount
					c.Writer.Write(ansihtml.ConvertToHTML(newContent)) //nolint:errcheck,gosec
					c.Writer.Flush()
				}
				finished, _ := script.File(wd + `/` + "skywire-cli-log.txt").Last(1).MatchRegexp(regexp.MustCompile(".*finished.*")).String() //nolint:errcheck,gosec
				if finished != "" {
					break
				}
			}

			c.Writer.Write([]byte(htmltoplink)) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(htmlend)) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		r1.GET("/log-collection/tree", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Write([]byte("<!doctype html><html lang=en><head><meta charset='UTF-8'><title>Index of Skywire Surveys & Transport Logs</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(navlinks)) //nolint:errcheck,gosec
			c.Writer.Flush()
			surveycount, _ := script.FindFiles(wd + `/` + "log_backups/").Match("node-info.json").CountLines() //nolint:errcheck,gosec
			c.Writer.Write([]byte(fmt.Sprintf("Total surveys: %v\n", surveycount)))                            //nolint:errcheck,gosec,staticcheck
			c.Writer.Flush()
			st, err := script.Exec(`skywire cli log st -d ` + wd + `/log_backups -r`).Bytes()
			if err != nil {
				log.WithError(err).Error()
				c.Writer.Write([]byte(err.Error())) //nolint:errcheck,gosec
			}
			c.Writer.Write(ansihtml.ConvertToHTML(st)) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(htmltoplink)) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(htmlend)) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		r1.GET("/log-collection/tree/:pk", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			if c.Param("pk") == "" {
				c.Writer.WriteHeader(http.StatusBadRequest)
				c.Writer.Write([]byte("must specify public key")) //nolint:errcheck,gosec
				c.Writer.Flush()
				return
			}
			pks := strings.Split(c.Param("pk"), ",")
			for _, pk := range pks {
				var pK cipher.PubKey
				err := pK.Set(pk)
				if err != nil {
					c.Writer.WriteHeader(http.StatusBadRequest)
					c.Writer.Write([]byte("invalid public key: " + pk + " " + err.Error())) //nolint:errcheck,gosec
					c.Writer.Flush()
					return
				}
			}
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Write([]byte("<!doctype html><html lang=en><head><meta charset='UTF-8'><title>Index of Skywire Surveys & Transport Logs</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(navlinks)) //nolint:errcheck,gosec
			c.Writer.Flush()
			surveycount, _ := script.FindFiles(wd + `/` + "log_backups/").Match("node-info.json").CountLines() //nolint:errcheck,gosec
			c.Writer.Write([]byte(fmt.Sprintf("Total surveys: %v\n", surveycount)))                            //nolint:errcheck,gosec,staticcheck
			c.Writer.Flush()
			st, _ := script.Exec(`skywire cli log st -d rewards/log_backups -rup ` + c.Param("pk")).Bytes() //nolint:errcheck,gosec
			c.Writer.Write(ansihtml.ConvertToHTML(st))                                                      //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(htmltoplink)) //nolint:errcheck,gosec  //nolint:errcheck,gosec
			c.Writer.Flush()
			c.Writer.Write([]byte(htmlend)) //nolint:errcheck,gosec  //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		r1.GET("/log-collection/tplogs", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Write([]byte(func() (l string) { //nolint:errcheck,gosec  //nolint:errcheck,gosec
				l = "<!doctype html><html lang=en><head><title>Skywire Transport Bandwidth Logs By Day</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"
				l += navlinks
				l += "<p style='color:blue'>Blue = Verified Bandwidth</p>"
				l += "<p style='color:yellow'>Yellow = Transport bandwidth inconsistent</p>"
				l += "<p style='color:red'>Red = Error: sent or received is zero</p>"
				tp, _ := script.Exec(`skywire cli log tp -d rewards/log_backups`).String() //nolint:errcheck,gosec
				l += fmt.Sprintf("%s\n", ansihtml.ConvertToHTML([]byte(tp)))
				l += htmltoplink
				l += htmlend
				return l
			}()))
		})

		r1.GET("/stats/ut", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.Header().Set("Content-Type", "text/plain")
			c.Writer.Flush()
			utstats, err := script.Exec(`skywire cli ut -t`).String()
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				log.Println(err.Error())
				c.Writer.Flush()
				return
			}
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			c.Writer.Write([]byte(fmt.Sprintf("Uptime tracker version statistics:\n%s\n", utstats))) //nolint:errcheck,gosec,staticcheck
			c.Writer.Flush()
		})

		r1.StaticFile("/stats/arch", tempStatsPath+"/arch.txt")
		r1.StaticFile("/stats/os", tempStatsPath+"/os.txt")
		r1.StaticFile("/stats/cpu", tempStatsPath+"/cpu.txt")
		r1.StaticFile("/stats/mem", tempStatsPath+"/mem.txt")
		r1.StaticFile("/stats/ram", tempStatsPath+"/ram.txt")
		r1.StaticFile("/stats/country/unique", tempStatsPath+"/country_unique.txt")
		r1.StaticFile("/stats/country/unique/json", tempStatsPath+"/country_unique.json")
		r1.StaticFile("/stats/country/full", tempStatsPath+"/country_full.txt")
		r1.StaticFile("/stats/country/full/json", tempStatsPath+"/country_full.json")

		// Aggregated stats page
		r1.GET("/stats", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			c.Writer.WriteHeader(http.StatusOK)

			l := "<html><head><title>Network Statistics</title></head>"
			l += "<body style='background-color:black;color:white;font-family:monospace;'>"
			l += "<a id='top'></a>"
			l += navlinks
			l += "<h1>Skywire Network Statistics</h1>"

			// Uptime Tracker Version Stats
			l += "<h2><a href='/stats/ut'>Uptime Tracker Version Statistics</a></h2>"
			l += "<pre>"
			utstats, err := script.Exec(`skywire cli ut -t`).String()
			if err == nil {
				l += utstats
			} else {
				l += "Error fetching uptime tracker stats"
			}
			l += "</pre>"

			// Architecture Stats
			l += "<h2><a href='/stats/arch'>Architecture Statistics</a></h2>"
			l += "<pre>"
			archStats, err := script.File(tempStatsPath + "/arch.txt").String()
			if err == nil {
				l += archStats
			} else {
				l += "Error loading architecture stats"
			}
			l += "</pre>"

			// OS Stats
			l += "<h2><a href='/stats/os'>Operating System Statistics</a></h2>"
			l += "<pre>"
			osStats, err := script.File(tempStatsPath + "/os.txt").String()
			if err == nil {
				l += osStats
			} else {
				l += "Error loading OS stats"
			}
			l += "</pre>"

			// CPU Stats
			l += "<h2><a href='/stats/cpu'>CPU Statistics</a></h2>"
			l += "<pre>"
			cpuStats, err := script.File(tempStatsPath + "/cpu.txt").String()
			if err == nil {
				l += cpuStats
			} else {
				l += "Error loading CPU stats"
			}
			l += "</pre>"

			// Memory (Disk) Stats
			l += "<h2><a href='/stats/mem'>Storage Statistics</a></h2>"
			l += "<pre>"
			memStats, err := script.File(tempStatsPath + "/mem.txt").String()
			if err == nil {
				l += memStats
			} else {
				l += "Error loading storage stats"
			}
			l += "</pre>"

			// RAM Stats
			l += "<h2><a href='/stats/ram'>RAM Statistics</a></h2>"
			l += "<pre>"
			ramStats, err := script.File(tempStatsPath + "/ram.txt").String()
			if err == nil {
				l += ramStats
			} else {
				l += "Error loading RAM stats"
			}
			l += "</pre>"

			// Country Stats - Unique IPs
			l += "<h2><a href='/stats/country/unique'>Country Statistics (Unique IPs)</a></h2>"
			l += "<pre>"
			countryUniqueStats, err := script.File(tempStatsPath + "/country_unique.txt").String()
			if err == nil {
				l += countryUniqueStats
			} else {
				l += "Error loading country stats (unique)"
			}
			l += "</pre>"
			l += "<p><a href='/stats/country/unique/json'>View as JSON</a></p>"

			// Country Stats - Full Visor Count
			l += "<h2><a href='/stats/country/full'>Country Statistics (All Visors)</a></h2>"
			l += "<pre>"
			countryFullStats, err := script.File(tempStatsPath + "/country_full.txt").String()
			if err == nil {
				l += countryFullStats
			} else {
				l += "Error loading country stats (full)"
			}
			l += "</pre>"
			l += "<p><a href='/stats/country/full/json'>View as JSON</a></p>"

			l += "<br>" + htmltoplink
			l += "</body></html>"

			c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
		})

		r1.GET("/skycoin-rewards", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			l := fmt.Sprintf("<div style='float: right;'>%s</div>", func() string {
				yearlyTotal := 408000.0
				result := fmt.Sprintf("<u>Annual reward distribution per pool:</u>\n%g Skycoin\n<u>Monthly rewards per pool:</u>\n", yearlyTotal)
				currentMonth := time.Now().Month()
				currentYear := time.Now().Year()
				for month := time.January; month <= time.December; month++ {
					daysInMonth := time.Date(currentYear, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
					monthlyRewards := (yearlyTotal / 365) * float64(daysInMonth)
					format := "%g %d %s\n"
					if currentMonth >= month {
						format = "<strike>" + format + "</strike>"
					}
					result += fmt.Sprintf(format, monthlyRewards, currentYear, month)
				}
				firstDayOfNextYear := time.Date(currentYear+1, time.January, 1, 0, 0, 0, 0, time.UTC)
				lastDayOfYear := firstDayOfNextYear.Add(-time.Second)
				totalDaysInYear := lastDayOfYear.YearDay()
				skycoinPerDay := yearlyTotal / float64(totalDaysInYear)
				result += fmt.Sprintf("%g Skycoin per day\n<br>", skycoinPerDay)
				return result
			}())
			l += fmt.Sprintf("There are %d days in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day(), time.Now().Month())
			l += fmt.Sprintf("Today is %s %d.\n", time.Now().Month(), time.Now().Day())
			l += fmt.Sprintf("There are %d days left in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()-time.Now().Day(), time.Now().Month())
			l += fmt.Sprintf("%d days in the year %d.\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay(), time.Now().Year())
			l += fmt.Sprintf("Today is day %d.\n", time.Now().YearDay())
			l += fmt.Sprintf("There are %d days remaining in %d<br>", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay()-time.Now().YearDay(), time.Now().Year())
			calendar, err := script.Exec(`bash -c 'set -o pipefail ; unbuffer cal --color | lolcat -f -F 0.5'`).String()
			if err != nil {
				calendar = cal()
			}
			l += "\n" + string(ansihtml.ConvertToHTML([]byte(calendar)))
			l += "\n\n<table style='border-collapse: collapse; width: auto;'>\n"
			l += "\n\n<table style='border-collapse: collapse; width: auto;'>\n"
			l += "<thead>\n"
			l += "<tr>\n"
			l += "<th style='text-align: center;'> <br> <u>RewardDate</u> </th><th style='text-align: center;'> Pool 1 <br> <u>SKY/VISOR</u> </th><th style='text-align: center;'> Pool 2 <br> <u>SKY/VISOR</u> </th><th style='text-align: center;'> Distributed <br> <u>[<span style='color: red;'>&#10060;</span>/<span style='color: green;'>&#10004;</span>]</u> </th>\n"
			l += "</tr>\n"
			l += "</thead>\n"
			l += "<tbody>\n"
			rewardtxncsvs, _ := script.FindFiles(wd+`/hist`).MatchRegexp(regexp.MustCompile(".?.?.?.?-.?.?-.?.?_rewardtxn0.csv")).Replace(wd+"/hist/", "").Replace("_rewardtxn0.csv", "").Slice() //nolint:errcheck,gosec
			for i := len(rewardtxncsvs) - 1; i >= 0; i-- {
				skycoinpershare, _ := script.File(wd+"/hist/"+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share: ").Replace("Skycoin Per Share: ", "").String() //nolint:errcheck,gosec  //nolint:errcheck,gosec
				skycoinpershare1 := ""
				skycoinpershare2 := ""
				if strings.TrimSpace(skycoinpershare) == "" {
					skycoinpershare1, _ = script.File(wd+"/hist/"+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 1): ").Replace("Skycoin Per Share (Pool 1): ", "").String() //nolint:errcheck,gosec  //nolint:errcheck,gosec
					skycoinpershare2, _ = script.File(wd+"/hist/"+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 2): ").Replace("Skycoin Per Share (Pool 2): ", "").String() //nolint:errcheck,gosec  //nolint:errcheck,gosec
					skycoinpershare1 = strings.TrimSpace(skycoinpershare1)
					skycoinpershare2 = strings.TrimSpace(skycoinpershare2)
				} else {
					skycoinpershare1 = strings.TrimSpace(skycoinpershare)
					skycoinpershare2 = ""
				}

				var distributedIcon string
				if _, err := os.Stat(wd + "/hist/" + rewardtxncsvs[i] + ".txt"); err == nil {
					distributedIcon = "<span style='color: green;'>&#10004;</span>"
				} else {
					distributedIcon = "<span style='color: red;'>&#10060;</span>"
				}
				l += "<tr>\n"
				l += "<td style='text-align: center;'><a href='/skycoin-rewards/hist/" + rewardtxncsvs[i] + "'>" + rewardtxncsvs[i] + "</a></td>\n"
				l += "<td style='text-align: center;'>" + skycoinpershare1 + "</td>\n"
				if skycoinpershare2 != "" {
					l += "<td style='text-align: center;'>" + skycoinpershare2 + "</td>\n"
				} else {
					l += "<td style='text-align: center;'></td>\n"
				}
				l += "<td style='text-align: center;'>" + distributedIcon + "</td>\n"
				l += "</tr>\n"
			}
			l += "</tbody>\n</table>\n"
			l += "<br>" + htmltoplink

			tmpl0, err1 := tmpl.Clone()
			if err1 != nil {
				fmt.Println("Error cloning template:", err1)
			}
			_, err1 = tmpl0.New("this").Parse(htmlRewardPageTemplate)
			if err1 != nil {
				fmt.Println("Error parsing Front Page template:", err1)
			}
			tmpl := tmpl0
			htmlPageTemplateData1 := htmlTemplateData{
				Title:   "Skycoin Reward Calculation and Distribution",
				Content: htmpl.HTML(l), //nolint:gosec
			}
			tmplData := map[string]interface{}{
				"Page": htmlPageTemplateData1,
			}
			var result bytes.Buffer
			err = tmpl.Execute(&result, tmplData)
			if err != nil {
				fmt.Println("error: ", err)
			}

			c.Writer.Write(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1)) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		authRoute := r1.Group("/")
		if len(wlkeys) > 0 {
			authRoute.Use(whitelistAuth(wlkeys))
		}

		// dmsgpost dmsg://036a70e6956061778e1883e928c1236189db14dfd446df23d83e45c321b330c91f:80/reward -d $(skycoin-cli createRawTransaction /home/user/.skycoin/wallets/2023_06_29.wlt --csv <(curl --silent -L http://fiber.skywire.dev/skycoin-rewards/csv) -a 24MGsKPDo3EJX4uF1h4CHcgmNNHmtGaLR5f) -s <secret-key-of-reward-whitelisted-pk>
		authRoute.POST("/reward", func(c *gin.Context) {
			//override the behavior of `public fallback` for this endpoint
			if len(wlkeys) == 0 {
				c.Writer.WriteHeader(http.StatusUnauthorized)
				c.Writer.Write([]byte("len(wlkeys) == 0")) //nolint:errcheck,gosec
				return
			}
			// Read the request body
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				c.Writer.Write([]byte("io.ReadAll(c.Request.Body) :\n\n" + string(body) + "\n\nError:\n\n" + err.Error())) //nolint:errcheck,gosec
				return
			}
			//check that wallet is running
			status, err := script.Exec("skycoin-cli status").String()
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				c.Writer.Write([]byte("skycoin-cli status:\n\n" + status + "\n\nskycoin-cli status error:\n\n" + err.Error())) //nolint:errcheck,gosec
				return
			}
			//find all transacion csvs
			f, err := script.FindFiles(wd + `/hist/`).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice()
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				c.Writer.Write([]byte(`script.FindFiles(wd + /hist/).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice():\n\n` + strings.Join(f, "\n") + "\n\nError:\n\n" + err.Error())) //nolint:errcheck,gosec
				return
			}
			//and range through the results
			for _, f1 := range f {
				//look for .txt file with the same date
				g, err := script.File(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String()
				//error is expected here - file does not exist when rewards have not been distributed for that _rewardtxn0.csv
				//also consider rewards not distributed if the file exists but is empty or contains "test" - for testing
				if err != nil || g == "" || g == "\n" || g == "test" || g == "test\n" {
					//raw transaction is the request body ; decode it to make sure it's good
					decoded, err := script.Exec("skycoin-cli decodeRawTransaction " + string(body)).String()
					if err != nil {
						c.Writer.WriteHeader(http.StatusBadRequest)
						c.Writer.Write([]byte("skycoin-cli decodeRawTransaction:\n\n" + decoded + "\n\nskycoin-cli decodeRawTransaction error:\n\n" + err.Error())) //nolint:errcheck,gosec
						return
					}
					//if all is well, broadcast the transaction
					txid, err := script.Exec("skycoin-cli broadcastTransaction " + string(body)).String()
					if err != nil {
						c.Writer.WriteHeader(http.StatusInternalServerError)
						c.Writer.Write([]byte("skycoin-cli broadcastTransaction:\n\n" + txid + "\n\nskycoin-cli broadcastTransaction error:\n\n" + err.Error())) //nolint:errcheck,gosec
						return
					}
					//record the transaction ID for that day's reward
					_, err = script.Echo(txid).WriteFile(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1))
					if err != nil {
						c.Writer.WriteHeader(http.StatusInternalServerError)
						c.Writer.Write([]byte(`script.Echo(txid).WriteFile(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1))\n\n` + txid + "\n\n" + strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1) + "\n\nerror:\n\n" + err.Error())) //nolint:errcheck,gosec
						return
					}
					//record the transaction ID for the reward notification system - append the file!
					_, err = script.Echo(txid).AppendFile(wd + `/` + "transactions0.txt")
					if err != nil {
						c.Writer.WriteHeader(http.StatusInternalServerError)
						c.Writer.Write([]byte(`script.Echo(txid).AppendFile(wd + / + "transactions0.txt")\n\n` + txid + "\n\nerror:\n\n" + err.Error())) //nolint:errcheck,gosec
						return
					}
					c.Writer.WriteHeader(http.StatusOK)
					c.Writer.Write([]byte(txid)) //nolint:errcheck,gosec
					return
				}
			}
			c.Writer.WriteHeader(http.StatusNotFound)
			h, _ := script.FindFiles(wd + `/hist/`).String()                      //nolint:errcheck,gosec
			c.Writer.Write([]byte("No undistributed rewards csv found.\n\n" + h)) //nolint:errcheck,gosec
		})

		r1.GET("/skycoin-rewards/csv", func(c *gin.Context) {
			active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint:errcheck,gosec
			if strings.TrimRight(active, "\n") == "active" {
				c.Writer.Header().Set("Server", "")
				c.Writer.WriteHeader(http.StatusNotFound)
				return
			}
			c.Writer.Header().Set("Server", "")
			f, _ := script.FindFiles(wd + `/hist/`).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Basename().Slice() //nolint:errcheck,gosec
			for _, f1 := range f {
				g, err := script.File(wd + `/hist/` + strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String()
				if err != nil || g == "" || g == "\n" || g == "test" || g == "test\n" {
					c.Writer.Header().Set("Content-Type", "text/plain")
					c.Writer.WriteHeader(http.StatusOK)
					c.Writer.Write([]byte("skycoin-rewards/hist/" + f1)) //nolint:errcheck,gosec
					return
				}

			}
			c.Writer.WriteHeader(http.StatusNotFound)
		})
		//status of reward system hourly run.
		r1.GET("/skycoin-rewards/s", func(c *gin.Context) {
			active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint:errcheck,gosec
			c.JSON(http.StatusOK, gin.H{"active": strings.TrimRight(active, "\n")})
		})

		r1.GET("/skycoin-rewards/csv/plain", func(c *gin.Context) {
			active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint:errcheck,gosec
			if strings.TrimRight(active, "\n") == "active" {
				c.Writer.Header().Set("Server", "")
				c.Writer.WriteHeader(http.StatusNotFound)
				return
			}
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/plain")
			f, _ := script.FindFiles(wd + `/hist/`).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Basename().Slice() //nolint:errcheck,gosec
			for _, f1 := range f {
				g, _ := script.File(wd + `/hist/` + strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String() //nolint:errcheck,gosec
				if g != "" && g != "\n" {
					c.Redirect(http.StatusFound, "/skycoin-rewards/hist/"+f1)
					return
				}

			}
			c.Writer.WriteHeader(http.StatusNotFound)
		})

		r1.GET("/skycoin-rewards/hist/:date", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			_, err := time.Parse("2006-01-02", c.Param("date"))
			if err != nil {
				_, err1 := time.Parse("2006-01-02", strings.Replace(c.Param("date"), "_rewardtxn0.csv", "", -1))
				_, err2 := time.Parse("2006-01-02", strings.Replace(c.Param("date"), "_stats.txt", "", -1))
				_, err3 := time.Parse("2006-01-02", strings.Replace(c.Param("date"), "_ineligible.csv", "", -1))
				_, err4 := time.Parse("2006-01-02", strings.Replace(c.Param("date"), "_shares.csv", "", -1))
				_, err5 := time.Parse("2006-01-02", strings.Replace(c.Param("date"), ".txt", "", -1))
				if err1 != nil && err2 != nil && err3 != nil && err4 != nil && err5 != nil {
					fmt.Println("cant parse date or match filename")
					c.Writer.WriteHeader(http.StatusNotFound)
					c.Writer.Flush()
					return
				}
				if err1 == nil || err2 == nil || err5 == nil {
					filetoserve, err := script.File(wd + `/hist/` + c.Param("date")).Bytes()
					if err == nil {
						c.Writer.Header().Set("Content-Type", "text/plain")
						c.Writer.WriteHeader(http.StatusOK)
						c.Writer.Flush()
						_, _ = c.Writer.Write(filetoserve) //nolint:errcheck,gosec
						c.Writer.Flush()
						return
					}
					fmt.Println("non nil script.File error")
					c.Writer.WriteHeader(http.StatusNotFound)
					c.Writer.Flush()
					return
				}
				if err3 == nil {
					l2, err := script.File(wd + `/hist/` + c.Param("date")).Slice()
					if err != nil {
						fmt.Println("non nil script.File error")
						c.Writer.WriteHeader(http.StatusNotFound)
						c.Writer.Flush()
						return
					}
					var toserve string
					for _, line := range l2 {
						thispk, _ := script.Echo(line).Column(2).String() //nolint:errcheck,gosec
						reason, _ := script.Echo(line).Column(3).String() //nolint:errcheck,gosec
						toserve += fmt.Sprintf("%s%s\n", strings.TrimRight(strings.TrimRight(thispk, "\n"), "\r"), strings.TrimRight(strings.TrimRight(strings.TrimRight(reason, "\n"), "\r"), ","))
					}
					c.Writer.Header().Set("Content-Type", "text/plain")
					c.Writer.WriteHeader(http.StatusOK)
					c.Writer.Flush()
					c.Writer.Write([]byte(toserve)) //nolint:errcheck,gosec
					c.Writer.Flush()
					return
				}
				if err4 == nil {
					l2, err := script.File(wd + `/hist/` + c.Param("date")).Slice()
					if err != nil {
						fmt.Println("non nil script.File error")
						c.Writer.WriteHeader(http.StatusNotFound)
						c.Writer.Flush()
						return
					}
					var toserve string
					for i, line := range l2 {
						if i == 0 {
							continue
						}
						thispk, err := script.Echo(line).Column(2).String()
						if err != nil {
							fmt.Println("non nil script.Echo(line).Column(2).String() error")
							c.Writer.WriteHeader(http.StatusNotFound)
							c.Writer.Flush()
							return
						}
						share, err := script.Echo(line).Column(3).String()
						if err != nil {
							fmt.Println("non nil script.Echo(line).Column(3).String() error")
							c.Writer.WriteHeader(http.StatusNotFound)
							c.Writer.Flush()
							return
						}
						sky, err := script.Echo(line).Column(4).String()
						if err != nil {
							fmt.Println("non nil script.Echo(line).Column(4).String() error")
							c.Writer.WriteHeader(http.StatusNotFound)
							c.Writer.Flush()
							return
						}
						toserve += fmt.Sprintf("%s%s%s\n", strings.TrimRight(strings.TrimRight(thispk, "\n"), "\r"), strings.TrimRight(strings.TrimRight(share, "\n"), "\r"), strings.TrimRight(strings.TrimRight(strings.TrimRight(sky, "\n"), "\r"), ","))
					}
					c.Writer.Header().Set("Content-Type", "text/plain")
					c.Writer.WriteHeader(http.StatusOK)
					c.Writer.Flush()
					c.Writer.Write([]byte(toserve)) //nolint:errcheck,gosec
					c.Writer.Flush()
					return
				}

			}
			rewardfiles, err := script.FindFiles(wd + `/hist`).Match(c.Param("date")).Slice()
			if err != nil {
				fmt.Println("non nil script.FindFiles(wd + `/hist`).Match(c.Param(\"date\")).Slice() error")
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			if len(rewardfiles) == 0 {
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			l := ""
			l3, err := os.Stat(wd + `/hist/` + c.Param("date") + "_rewardtxn0.csv")
			if err != nil {
				fmt.Println("non nil os.Stat(wd + `/hist/` + c.Param(\"date\") + \"_rewardtxn0.csv\") error")
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			l += "Reward data generated: " + l3.ModTime().Format("2006-01-02 15:04:05") + "\n\n"

			l1, err := script.File(wd + `/hist/` + c.Param("date") + ".txt").String()
			if err != nil {
				l += "Rewards not distributed yet\n\n"
			} else {
				if l1 == "" {
					l += "Reward txid not recorded\n\n"
				} else {
					l += "Reward TXID:\n" + l1 + "\n\n"
					l += "Explorer link:\n<a href='https://explorer.skycoin.com/app/transaction/" + l1 + "''>" + l1 + "</a>\n\n"
				}
			}

			l2, err := script.File(wd + `/hist/` + c.Param("date") + "_shares.csv").Slice()
			if err != nil {
				l += "<div style='float: right;'>PK,Share,SKY Amount\nReward shares file not found\nerror: " + err.Error() + "\n\n"
			} else {
				l += "<div style='float: right;'>PK,Share,SKY Amount\n"
				for i, line := range l2 {
					if i == 0 {
						continue
					}
					thispk, err := script.Echo(line).Column(2).String()
					if err != nil {
						fmt.Println("non nil script.Echo(line).Column(2).String() error")
						c.Writer.WriteHeader(http.StatusNotFound)
						c.Writer.Flush()
						return
					}
					share, err := script.Echo(line).Column(3).String()
					if err != nil {
						fmt.Println("non nil script.Echo(line).Column(3).String() error")
						c.Writer.WriteHeader(http.StatusNotFound)
						c.Writer.Flush()
						return
					}
					sky, err := script.Echo(line).Column(4).String()
					if err != nil {
						fmt.Println("non nil script.Echo(line).Column(4).String() error")
						c.Writer.WriteHeader(http.StatusNotFound)
						c.Writer.Flush()
						return
					}
					l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + strings.TrimRight(share, "\n") + strings.Replace(sky, ",\n", "\n", -1)
				}
			}
			l2, err = script.File(wd + `/hist/` + c.Param("date") + "_ineligible.csv").Slice()
			if err == nil {
				l += "\n\nIneligible:\n"
				for _, line := range l2 {
					thispk, _ := script.Echo(line).Column(2).String()         //nolint:errcheck,gosec
					reason, _ := script.Echo(line).Column(3).String()         //nolint:errcheck,gosec
					invalid, _ := script.Echo(line).Match(", , , ,").String() //nolint:errcheck,gosec
					if invalid != "" {
						_, err = script.IfExists(wd + `/` + "log_backups/" + thispk + "/node-info.json").Echo("").String()
						if err != nil {
							l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + " Survey not found\n"
						} else {
							l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + " Invalid survey\n"
						}
					} else {
						l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + " Ineligible " + strings.Replace(reason, ",\n", "\n", -1)
					}
				}
			}
			l += "</div>"

			l1, err = script.File(wd + `/hist/` + c.Param("date") + "_stats.txt").String()
			if err != nil {
				fmt.Println("non nil script.File(wd + `/hist/` + c.Param(\"date\") + \"_stats.txt\").String() error")
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			l += c.Param("date") + "_stats.txt\n" + l1 + "\n"

			l2, err = script.File(wd+`/`+"hist/"+c.Param("date")+"_rewardtxn0.csv").Replace(",", " ").Slice()
			if err != nil {
				fmt.Println("non nil script.File(wd+`/`+\"hist/\"+c.Param(\"date\")+\"_rewardtxn0.csv\").Replace(\",\", \" \").Slice() error")
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			l += c.Param("date") + "_transaction0.csv\n\nSKY Address, Amount\n"
			for _, line := range l2 {
				skyaddr, err := script.Echo(line).Column(1).String()
				if err != nil {
					fmt.Println("non nil script.Echo(line).Column(1).String() error")
					c.Writer.WriteHeader(http.StatusNotFound)
					c.Writer.Flush()
					return
				}
				skyamt, err := script.Echo(line).Column(2).String()
				if err != nil {
					fmt.Println("non nil script.Echo(line).Column(2).String() error")
					c.Writer.WriteHeader(http.StatusNotFound)
					c.Writer.Flush()
					return
				}
				l += "<a id='" + strings.TrimRight(skyaddr, "\n") + "'>" + strings.TrimRight(skyaddr, "\n") + "</a>," + strings.TrimRight(skyamt, "\n") + "\n"
			}

			l += "<br>" + htmltoplink
			tmpl0, err1 := tmpl.Clone()
			if err1 != nil {
				fmt.Println("Error cloning template:", err1)
			}
			_, err1 = tmpl0.New("this").Parse(htmlRewardPageTemplate)
			if err1 != nil {
				fmt.Println("Error parsing Front Page template:", err1)
			}
			tmpl := tmpl0
			htmlPageTemplateData1 := htmlTemplateData{
				Title:   "Skycoin Reward Calculation and Distribution",
				Content: htmpl.HTML(l), //nolint:gosec
			}
			//	htmlPageTemplateData1.Content =
			tmplData := map[string]interface{}{
				"Page": htmlPageTemplateData1,
			}
			var result bytes.Buffer
			err = tmpl.Execute(&result, tmplData)
			if err != nil {
				fmt.Println("error: ", err)
			}

			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			c.Writer.Write(result.Bytes()) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		authRoute.GET("/node-info/:pk", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			//override the behavior of `public fallback` for this endpoint
			if len(wlkeys) == 0 {
				c.Writer.WriteHeader(http.StatusUnauthorized)
				c.Writer.Write([]byte("len(wlkeys) == 0")) //nolint:errcheck,gosec
				return
			}
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
			ni, err := script.File(wd + `/` + "log_backups/" + c.Param("pk") + "/node-info.json").Bytes()
			if err != nil {
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
			_, _ = c.Writer.Write(ni) //nolint:errcheck,gosec
			c.Writer.Flush()
		})

		type reward struct {
			Date string  `json:"date"`
			One  float64 `json:"1"`
			Two  float64 `json:"2"`
			Sent string  `json:"sent"`
		}

		type rewards []reward

		r1.GET("/skycoin-rewards.json", func(c *gin.Context) {
			data := rewards{}
			rewardtxncsvs, err := script.FindFiles(wd+`/hist`).MatchRegexp(regexp.MustCompile(".?.?.?.?-.?.?-.?.?_rewardtxn0.csv")).Basename().Replace("_rewardtxn0.csv", "").Slice()
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				c.Writer.Write([]byte("500 Internal Server Error #1 " + err.Error())) //nolint:errcheck,gosec
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			counter := 0
			for i := len(rewardtxncsvs) - 1; i >= 0; i-- {
				if counter >= 90 {
					break
				}
				counter++
				var rdata reward
				rdata.Date = rewardtxncsvs[i]
				skycoinpershare, err := script.File(wd+`/hist/`+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share: ").Replace("Skycoin Per Share: ", "").String()
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte("500 Internal Server Error #2 " + err.Error())) //nolint:errcheck,gosec
					c.AbortWithStatus(http.StatusInternalServerError)
					return
				}
				if strings.TrimSpace(skycoinpershare) == "" {
					pool1, err := script.File(wd+`/hist/`+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 1): ").Replace("Skycoin Per Share (Pool 1): ", "").String()
					if err != nil {
						c.Writer.WriteHeader(http.StatusInternalServerError)
						c.Writer.Write([]byte("500 Internal Server Error #3 " + err.Error())) //nolint:errcheck,gosec
						c.AbortWithStatus(http.StatusInternalServerError)
						return
					}
					rdata.One, err = strconv.ParseFloat(strings.TrimRight(pool1, "\n"), 64)
					if err != nil {
						rdata.One = 0.0
					}
					pool2, err := script.File(wd+`/hist/`+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 2): ").Replace("Skycoin Per Share (Pool 2): ", "").String()
					if err != nil {
						c.Writer.WriteHeader(http.StatusInternalServerError)
						c.Writer.Write([]byte("500 Internal Server Error #5 " + err.Error())) //nolint:errcheck,gosec
						c.AbortWithStatus(http.StatusInternalServerError)
						return
					}
					rdata.Two, err = strconv.ParseFloat(strings.TrimRight(pool2, "\n"), 64)
					if err != nil {
						rdata.Two = 0.0
					}
				} else {
					rdata.One, err = strconv.ParseFloat(skycoinpershare, 64)
					if err != nil {
						rdata.One = 0.0
					}
				}
				//			rdata.Sent = "❌"
				rdata.Sent = "false"
				if _, err := os.Stat(wd + `/hist/` + rewardtxncsvs[i] + ".txt"); err == nil {
					//				rdata.Sent = "✔"
					rdata.Sent = "true"
				}
				data = append(data, rdata)
			}
			c.Header("Content-Type", "application/json")
			c.JSON(http.StatusOK, data)
		})

		r1.GET("/skycoin-rewards/txids", func(c *gin.Context) {
			txids, err := script.File(wd + "/transactions0.txt").Slice()
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				c.Writer.Write([]byte("500 Internal Server Error" + err.Error())) //nolint:errcheck,gosec
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			c.Header("Content-Type", "application/json")
			c.JSON(http.StatusOK, txids)
		})

		r1.StaticFile("/log-collection/json", filepath.Join(os.TempDir(), "log-collection.json"))

		r1.GET("/favicon.ico", func(c *gin.Context) {
			_, _ = c.Writer.WriteString(string(faviconBuffer)) //nolint:errcheck,gosec
		})

		if e != nil {
			r1.GET("/", mainPage)
			r1.GET("/index.html", mainPage)
		} else {
			//manually create routes to the compiled cogentcore web app source files
			filepath.Walk(outputDir+"/bin/web", func(path string, info os.FileInfo, err error) error { //nolint:errcheck,gosec,revive
				if !info.IsDir() {
					relPath, err := filepath.Rel(outputDir+"/bin/web", path)
					if err != nil {
						return err
					}

					if strings.HasSuffix(relPath, "index.html") {
						r1.GET("/", func(c *gin.Context) {
							c.File(path)
						})
					} else {
						r1.GET("/"+relPath, func(c *gin.Context) {
							c.File(path)
						})
					}
				}
				return nil
			})

		}
		// Start the server using the custom Gin handler
	}

	// Increased timeouts for dmsg latency characteristics
	// DMSG has higher latency than direct TCP due to multi-hop routing
	serve := &http.Server{
		Handler:           &ginHandler{Router: r1},
		ReadTimeout:       30 * time.Second, // Allow for dmsg multi-hop latency
		WriteTimeout:      60 * time.Second, // Allow time to generate large responses
		IdleTimeout:       90 * time.Second, // Keep connections alive longer
		ReadHeaderTimeout: 10 * time.Second, // Headers can be slow over dmsg
	}

	wg := new(sync.WaitGroup)
	// Start serving
	wg.Add(1)
	go func() {
		fmt.Printf("listening on http://127.0.0.1:%d using gin router\n", webPort)
		r1.Run(fmt.Sprintf(":%d", webPort)) //nolint:errcheck,gosec
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		log.WithField("dmsg_addr", lis.Addr().String()).Info("Serving...")
		if err := serve.Serve(lis); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Serve: %v", err)
		}
		wg.Done()
	}()

	// Graceful shutdown handler
	go func() {
		<-ctx.Done()
		log.Info("Shutting down dmsghttp reward server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := serve.Shutdown(shutdownCtx); err != nil {
			log.WithError(err).Error("HTTP server shutdown error")
		}
	}()

	if ensureOnlineURL != "" {
		go func() {
			var errCount int
			for range time.Tick(15 * time.Minute) {
				_, err := script.NewPipe().WithHTTPClient(&http.Client{Timeout: 60 * time.Second}).Get(ensureOnlineURL).AppendFile("/dev/null")
				if err != nil {
					errCount++
					log.WithError(err).Error(fmt.Sprintf("Error fetching %v\nError count: %v", ensureOnlineURL, errCount))
				} else {
					errCount = 0
				}
				if errCount >= 3 {
					log.Fatalf("http server %v unreachable after %v tries ; exiting", ensureOnlineURL, errCount)
				}
			}
		}()
	}

	go func() {
		err := generateAndCacheJSON()
		if err != nil {
			fmt.Println("Error updating log-collection cache:", err)
		}
		err = generateAndCacheStats()
		if err != nil {
			fmt.Println("Error updating statistics cache:", err)
		}
		for range time.NewTicker(cacheInterval).C {
			err := generateAndCacheJSON()
			if err != nil {
				fmt.Println("Error updating log-collection cache:", err)
			}
			err = generateAndCacheStats()
			if err != nil {
				fmt.Println("Error updating statistics cache:", err)
			}
		}
	}()

	wg.Wait()
}

func cal() (ret string) {
	today := time.Now()
	year, month, _ := today.Date()
	firstOfMonth := time.Date(year, month, 1, 0, 0, 0, 0, time.Local)
	startDayOfWeek := firstOfMonth.Weekday()
	numDays := time.Date(year, month+1, 0, 0, 0, 0, 0, time.Local).Day()
	header := fmt.Sprintf("%s %d", month.String(), year)
	headerWidth := 20
	padding := (headerWidth - len(header)) / 2
	ret += fmt.Sprintf("%*s%s%*s\n", padding, "", header, headerWidth-len(header)-padding, "")
	ret += "Su Mo Tu We Th Fr Sa\n"
	for i := 0; i < int(startDayOfWeek); i++ {
		ret += "   "
	}
	day := 1
	for day <= numDays {
		for i := int(startDayOfWeek); i < 7 && day <= numDays; i++ {
			if day == today.Day() {
				ret += fmt.Sprintf("\x1b[30;47m%2d\x1b[0m ", day)
			} else {
				ret += fmt.Sprintf("%2d ", day)
			}
			day++
		}
		ret += "\n"
		startDayOfWeek = 0
	}
	return ret
}

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error")) //nolint:errcheck,gosec
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		// Check if the remote PK is whitelisted.
		whitelisted := false
		if len(whitelistedPKs) == 0 {
			whitelisted = true
		} else {
			for _, whitelistedPK := range whitelistedPKs {
				if remotePK == whitelistedPK.String() {
					whitelisted = true
					break
				}
			}
		}
		if whitelisted {
			c.Next()
		} else {
			// Otherwise, return a 401 Unauthorized error.
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.Writer.Write([]byte("401 Unauthorized")) //nolint:errcheck,gosec
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}
}

const nextlogrun = `#!/bin/bash
_nextskywireclilogrun() {
	if systemctl is-active --quiet skywire-reward >/dev/null; then
		printf "%s" "$(systemctl status skywire-reward.service --lines=0 | grep active |  sed 's/Active: active (running) since/Running since:\n/g' )"
	else
		systemctl status skywire-reward.timer --lines=0 | head -n4 | tail -n1 | sed 's/Trigger:/Next Log Collection Run:\n/g'
		printf 'Previous Run%s\n' "$(systemctl status skywire-reward.service --lines=0 | grep -m1 'Duration')"
	fi
}
`
