// Package clirewards cmd/skywire-cli/commands/rewards/server.go
package clirewards

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	htmpl "html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	"github.com/gin-gonic/gin"
	"github.com/robert-nix/ansihtml"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

//go:embed ui/*
var embeddedFiles embed.FS

var outputDir string
var err error
var (
	startTime       = time.Now()
	runTime         time.Duration
	sk              cipher.SecKey
	dmsgDisc        string
	dmsgPort        uint16
	dmsgSess        int
	wl              string
	wd              string
	wlkeys          []cipher.PubKey
	webPort         uint
	ensureOnlineURL string
)

var skyenvfile = os.Getenv("SKYENV")

func init() {
	RootCmd.AddCommand(
		serverCmd,
	)
	serverCmd.CompletionOptions.DisableDefaultCmd = true
	serverCmd.Flags().UintVarP(&webPort, "port", "p", scriptExecUint("${WEBPORT:-80}"), "port to serve")
	serverCmd.Flags().Uint16VarP(&dmsgPort, "dport", "d", scriptExecUint16("${DMSGPORT:-80}"), "dmsg port to serve")
	serverCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", scriptExecInt("${DMSGSESSIONS:-1}"), "dmsg sessions")
	msg := "add whitelist keys, comma separated to permit POST of reward transaction to be broadcast"
	if scriptExecArray("${REWARDPKS[@]}") != "" {
		msg += "\n\r"
	}
	serverCmd.Flags().StringVarP(&wl, "wl", "w", scriptExecArray("${REWARDPKS[@]}"), msg)
	wd, err = os.Getwd()
	if err != nil {
		log.Fatal("Error getting current directory:", err)
	}
	serverCmd.Flags().StringVarP(&wd, "wd", "W", wd, "location of dir containing 'log_collection' & reward 'hist' dirs")
	serverCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", skywire.Prod.DmsgDiscovery, "dmsg discovery url")
	serverCmd.Flags().StringVarP(&ensureOnlineURL, "ensure-online", "O", scriptExecString("${ENSUREONLINE}"), "Exit when the specified URL cannot be fetched;\ni.e. https://fiber.skywire.dev")
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint
	}
	if scriptExecString("${DMSGHTTP_SK}") != "" {
		sk.Set(scriptExecString("${DMSGHTTP_SK}")) //nolint
	}
	serverCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
}

// serverCmd starts the reward system ui server
var serverCmd = &cobra.Command{
	Use:   "ui",
	Short: "reward system UI server",
	Long: "skycoin reward system user interface server and skywire network metrics:\n https://fiber.skywire.dev\n" + `
	┌─┐┬┌┐ ┌─┐┬─┐
	├┤ │├┴┐├┤ ├┬┘
	└  ┴└─┘└─┘┴└─
	` + func() string {
		if _, err := os.Stat(skyenvfile); err == nil { //nolint
			return `run the web application

skyenv file detected: ` + skyenvfile
		}
		return `run the web application

.conf file may also be specified with
SKYENV=/path/to/fiber.conf fiber run`
	}(),
	Run: func(_ *cobra.Command, _ []string) {
		outputDir, err = extractFiles()
		if err != nil {
			fmt.Println("Error extracting files:", err)
			return
		}
		fmt.Printf("All files successfully extracted to '%s'.\n", outputDir)
		_, err = script.Exec(`bash -c 'cd ` + outputDir + ` || exit 0 ; go mod init fiber.skywire.dev/ui ; go get github.com/skycoin/skywire@develop && go mod tidy && go mod vendor && go run cogentcore.org/core/cmd/core@main build web'`).Stdout()
		if err != nil {
			fmt.Println("Error:", err)
			return
		}

		server()
	},
}

func extractFiles() (string, error) {
	tempDir := os.TempDir() + "/ui"
	err := os.Mkdir(tempDir, 0755) //nolint
	if err != nil {
		toRemove, err := script.FindFiles(tempDir).Reject(tempDir + "/vendor").Slice()
		if err != nil {
			return "", err
		}
		for i := range toRemove {
			_, err := os.Stat(toRemove[i])
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			err = os.RemoveAll(toRemove[i])
			if err != nil {
				return "", err
			}
		}
		log.Println("Omitted vendor dir when cleaning directory:", tempDir)
		log.Println("Directory contents:")
		_, _ = script.FindFiles(tempDir).Stdout() //nolint
	}

	if err := fs.WalkDir(embeddedFiles, "ui", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to access path %s: %w", path, err)
		}
		relPath, err := filepath.Rel("ui", path)
		if err != nil {
			return fmt.Errorf("failed to calculate relative path for %s: %w", path, err)
		}
		outputPath := filepath.Join(tempDir, relPath)
		if d.IsDir() {
			return os.MkdirAll(outputPath, 0755) //nolint
		}
		content, err := embeddedFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}
		if err := os.WriteFile(outputPath, content, 0644); err != nil { //nolint
			return fmt.Errorf("failed to write file %s: %w", outputPath, err)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to extract files: %w", err)
	}
	return tempDir, nil
}

var htmlRewardPageTemplate = `
{{.Page.Content}}
`
var tmpl *htmpl.Template
var htmlPageTemplateData htmlTemplateData

// TODO: fix gocyclo error.
//
//gocyclo:ignore
func server() {

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

	lis, err := dmsgclient.Listen(uint16(dmsgPort)) //nolint: gosec
	if err != nil {
		log.WithError(err).Fatal()
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			log.WithError(err).Error()
		}
	}()

	r1 := gin.New()
	// Disable Gin's default logger middleware
	r1.Use(gin.Recovery())
	r1.Use(loggingMiddleware())
	r1.GET("/index.html", mainPage)
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
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Transport statistics</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks)) //nolint
		c.Writer.Flush()
		tpstats, _ := script.Exec("skywire cli tp tree -s").Bytes() //nolint
		c.Writer.Write(ansihtml.ConvertToHTML(tpstats))             //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend)) //nolint
		c.Writer.Flush()
	})

	/* //consumes excessive server resources when network is heavily transported*/
	r1.GET("/transports-map", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Transport Map</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks)) //nolint
		c.Writer.Flush()
		tpstats, _ := script.Exec("skywire cli tp tree -s").Match("Count of transports:").Replace("Count of transports: ", "").Replace("\n", "").String() //nolint
		tpcount, _ := strconv.Atoi(tpstats)                                                                                                               //nolint
		if tpcount < 400 {
			tpTree, _ := script.Exec("skywire cli tp tree").Bytes() //nolint
			c.Writer.Write(ansihtml.ConvertToHTML(tpTree))          //nolint
			c.Writer.Flush()
		} else {
			c.Writer.Write([]byte(fmt.Sprintf("Transport count: %v exceeds server resources to map", tpcount))) //nolint
			c.Writer.Flush()
		}
		c.Writer.Write([]byte(htmlend)) //nolint
		c.Writer.Flush()
	})

	r1.GET("/log-collection", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Survey and Transport Log Collection</title></head>")) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte("<body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n#latest-content-anchor {\n  visibility: hidden;\n}\n</style>\n<pre>")) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks)) //nolint
		c.Writer.Flush()
		tmpFile, err := os.CreateTemp(os.TempDir(), "*.sh")
		if err != nil {
			return
		}
		if err := tmpFile.Close(); err != nil {
			return
		}
		_, _ = script.Exec(`chmod +x ` + tmpFile.Name()).String()                                         //nolint
		_, _ = script.Echo(nextlogrun).WriteFile(tmpFile.Name())                                          //nolint
		res, _ := script.Exec(`bash -c 'source ` + tmpFile.Name() + ` ; _nextskywireclilogrun'`).String() //nolint
		os.Remove(tmpFile.Name())                                                                         //nolint
		c.Writer.Write([]byte(fmt.Sprintf("%s\n", res)))                                                  //nolint
		c.Writer.Flush()

		// Initial line count
		initialLineCount, _ := script.File(wd + `/` + "skywire-cli-log.txt").CountLines() //nolint
		// Read and print the initial lines
		initialContent, _ := script.File(wd + `/` + "skywire-cli-log.txt").First(initialLineCount).Bytes() //nolint
		c.Writer.Write(ansihtml.ConvertToHTML(initialContent))                                             //nolint
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
			currentLineCount, _ := script.File(wd + `/` + "skywire-cli-log.txt").CountLines() //nolint
			// Check if there are new lines
			if currentLineCount > initialLineCount {
				newContent, _ := script.File(wd + `/` + "skywire-cli-log.txt").Last(currentLineCount - initialLineCount).Bytes() //nolint
				initialLineCount = currentLineCount
				c.Writer.Write(ansihtml.ConvertToHTML(newContent)) //nolint
				c.Writer.Flush()
			}
			finished, _ := script.File(wd + `/` + "skywire-cli-log.txt").Last(1).MatchRegexp(regexp.MustCompile(".*finished.*")).String() //nolint
			if finished != "" {
				break
			}
		}

		c.Writer.Write([]byte(htmltoplink)) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend)) //nolint
		c.Writer.Flush()
	})

	r1.GET("/log-collection/tree", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><meta charset='UTF-8'><title>Index of Skywire Surveys & Transport Logs</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks)) //nolint
		c.Writer.Flush()
		surveycount, _ := script.FindFiles(wd + `/` + "log_backups/").Match("node-info.json").CountLines() //nolint
		c.Writer.Write([]byte(fmt.Sprintf("Total surveys: %v\n", surveycount)))                            //nolint
		c.Writer.Flush()
		st, err := script.Exec(`skywire cli log st -d rewards/log_backups -r`).Bytes() //nolint
		if err != nil {
			log.WithError(err).Error()
			c.Writer.Write([]byte(err.Error())) //nolint
		}
		c.Writer.Write(ansihtml.ConvertToHTML(st)) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmltoplink)) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend)) //nolint
		c.Writer.Flush()
	})

	r1.GET("/log-collection/tree/:pk", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		if c.Param("pk") == "" {
			c.Writer.WriteHeader(http.StatusBadRequest)
			c.Writer.Write([]byte("must specify public key")) //nolint
			c.Writer.Flush()
			return
		}
		pks := strings.Split(c.Param("pk"), ",")
		for _, pk := range pks {
			var pK cipher.PubKey
			err := pK.Set(pk)
			if err != nil {
				c.Writer.WriteHeader(http.StatusBadRequest)
				c.Writer.Write([]byte("invalid public key: " + pk + " " + err.Error())) //nolint
				c.Writer.Flush()
				return
			}
		}
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><meta charset='UTF-8'><title>Index of Skywire Surveys & Transport Logs</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>")) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks)) //nolint
		c.Writer.Flush()
		surveycount, _ := script.FindFiles(wd + `/` + "log_backups/").Match("node-info.json").CountLines() //nolint
		c.Writer.Write([]byte(fmt.Sprintf("Total surveys: %v\n", surveycount)))                            //nolint
		c.Writer.Flush()
		st, _ := script.Exec(`skywire cli log st -d rewards/log_backups -rup ` + c.Param("pk")).Bytes() //nolint
		c.Writer.Write(ansihtml.ConvertToHTML(st))                                                      //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmltoplink)) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend)) //nolint
		c.Writer.Flush()
	})

	r1.GET("/log-collection/tplogs", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Write([]byte(func() (l string) { //nolint
			l = "<!doctype html><html lang=en><head><title>Skywire Transport Bandwidth Logs By Day</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"
			l += navlinks
			l += "<p style='color:blue'>Blue = Verified Bandwidth</p>"
			l += "<p style='color:yellow'>Yellow = Transport bandwidth inconsistent</p>"
			l += "<p style='color:red'>Red = Error: sent or received is zero</p>"
			tp, _ := script.Exec(`skywire cli log tp -d rewards/log_backups`).String() //nolint
			l += fmt.Sprintf("%s\n", ansihtml.ConvertToHTML([]byte(tp)))
			l += htmltoplink
			l += htmlend
			return l
		}())) //nolint
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
			totalDaysInYear := int(lastDayOfYear.YearDay())
			skycoinPerDay := yearlyTotal / float64(totalDaysInYear)
			result += fmt.Sprintf("%g Skycoin per day\n<br>", skycoinPerDay)
			utstats, err := script.Exec(`skywire cli ut -t`).String()
			if err == nil {
				result += fmt.Sprintf("<u>Uptime tracker version statistics:</u>\n%s\n<br>", utstats)
			}
			nis, err := script.FindFiles(wd + `/` + "log_backups").Match("node-info.json").Slice() //nolint
			if err == nil {
				var surveyarches string
				for _, ni := range nis {
					surveyarch, err := script.File(ni).JQ(".go_arch").Replace(`"`, "").String()
					if err == nil {
						surveyarches += surveyarch
					}
				}
				archstats, err := script.Echo(surveyarches).Freq().String() //nolint
				if err == nil {
					result += fmt.Sprintf("<u>Survey architecture statistics:</u>\n%s\n<br>", archstats)
				}
				var surveyOSNames string
				for _, ni := range nis {
					surveyOSName, err := script.File(ni).JQ(".zcalusic_sysinfo.os.name").Replace(`"`, "").String()
					if err == nil {
						surveyOSNames += surveyOSName
					}
				}
				namestats, err := script.Echo(surveyOSNames).Freq().String() //nolint
				if err == nil {
					result += fmt.Sprintf("<u>Survey OS name statistics:</u>\n%s\n<br>", namestats)
				}
				var surveycpus string
				for _, ni := range nis {
					surveycpu, err := script.File(ni).JQ(".zcalusic_sysinfo.cpu.model").Replace(`"`, "").String()
					if err == nil {
						surveycpus += surveycpu
					}
				}
				cpustats, err := script.Echo(surveycpus).Freq().String() //nolint
				if err == nil {
					result += fmt.Sprintf("<u>Survey CPU statistics:</u>\n%s\n<br>", cpustats)
				}

				var totalBytes int64
				for _, ni := range nis {
					surveytbs, err := script.File(ni).JQ(".ghw_blockinfo.total_size_bytes").Reject("null").Replace(`"`, "").String()
					if err == nil {
						if surveytbs != "\n" && surveytbs != "" {
							byteValue, err := strconv.ParseInt(strings.TrimRight(surveytbs, "\n"), 10, 64)
							if err != nil {
								result += fmt.Sprintf("Non nil error from strconv.ParseInt: %v\n", err)
							}
							totalBytes += byteValue
						}
					}
				}

				// Get stats for terabytes and gigabytes
				bsstatsTB, _ := script.Exec(`bash -c 'jq '.ghw_blockinfo.total_size_bytes' rewards/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("G").Slice() //nolint
				bsstatsGB, _ := script.Exec(`bash -c 'jq '.ghw_blockinfo.total_size_bytes' rewards/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("T").Slice() //nolint
				formattedTotal, err := script.Echo(fmt.Sprintf("%d", totalBytes)).ExecForEach("numfmt --to=iec {{.}}").String()
				if err != nil {
					result += fmt.Sprintf("%v\n", err)
				}
				result += fmt.Sprintf("<u>Survey total byte size (cumulative):</u> %s\n", formattedTotal)
				result += "<u>Survey total byte size statistics:</u>\n"
				result += `<table style="width:100%; text-align:center;">` + "\n"
				result += "<tr><th>GB</th><th>TB</th></tr>\n"

				maxLen := len(bsstatsGB)
				if len(bsstatsTB) > maxLen {
					maxLen = len(bsstatsTB)
				}
				for i := 0; i < maxLen; i++ {
					result += "<tr>\n"
					if i < len(bsstatsGB) {
						result += fmt.Sprintf(`<td style="text-align:center;">%s</td>`+"\n", bsstatsGB[i])
					} else {
						result += `<td style="text-align:center;"></td>` + "\n" // Empty centered cell
					}
					if i < len(bsstatsTB) {
						result += fmt.Sprintf(`<td style="text-align:center;">%s</td>`+"\n", bsstatsTB[i])
					} else {
						result += `<td style="text-align:center;"></td>` + "\n" // Empty centered cell
					}
					result += "</tr>\n"
				}
				result += "</table>\n<br>"

				var totalramBytes int64
				for _, ni := range nis {
					surveymem, err := script.File(ni).JQ(".ghw_memoryinfo.total_usable_bytes").Reject("null").Replace(`"`, "").String()
					if err == nil {
						if surveymem != "\n" && surveymem != "" {
							byteValue, err := strconv.ParseInt(strings.TrimRight(surveymem, "\n"), 10, 64)
							if err != nil {
								result += fmt.Sprintf("Non nil error from strconv.ParseInt: %v\n", err)
							}
							totalramBytes += byteValue
						}
					}
				}

				statsMB, _ := script.Exec(`bash -c 'jq '.ghw_memoryinfo.total_usable_bytes' rewards/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("G").Slice() //nolint
				statsGB, _ := script.Exec(`bash -c 'jq '.ghw_memoryinfo.total_usable_bytes' rewards/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("M").Slice() //nolint
				ramTotal, err := script.Echo(fmt.Sprintf("%d", totalramBytes)).ExecForEach("numfmt --to=iec {{.}}").String()
				if err != nil {
					result += fmt.Sprintf("%v\n", err)
				}
				result += fmt.Sprintf("<u>Survey total RAM byte size (cumulative):</u> %s\n", ramTotal)
				result += "<u>Survey total usable ram byte size statistics:</u>\n"
				result += `<table style="width:100%; text-align:center;">` + "\n"
				result += "<tr><th>GB</th><th>MB</th></tr>\n"

				maxLen = len(statsGB)
				if len(statsMB) > maxLen {
					maxLen = len(statsMB)
				}
				for i := 0; i < maxLen; i++ {
					result += "<tr>\n"
					if i < len(statsGB) {
						result += fmt.Sprintf(`<td style="text-align:center;">%s</td>`+"\n", statsGB[i])
					} else {
						result += `<td style="text-align:center;"></td>` + "\n" // Empty centered cell
					}
					if i < len(statsMB) {
						result += fmt.Sprintf(`<td style="text-align:center;">%s</td>`+"\n", statsMB[i])
					} else {
						result += `<td style="text-align:center;"></td>` + "\n" // Empty centered cell
					}
					result += "</tr>\n"
				}
				result += "</table>\n<br>"

			}
			return result + "<br>" + htmltoplink

		}())
		l += fmt.Sprintf("There are %d days in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day(), time.Now().Month())
		l += fmt.Sprintf("Today is %s %d.\n", time.Now().Month(), time.Now().Day())
		l += fmt.Sprintf("There are %d days left in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()-time.Now().Day(), time.Now().Month())
		l += fmt.Sprintf("%d days in the year %d.\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay(), time.Now().Year())
		l += fmt.Sprintf("Today is day %d.\n", time.Now().YearDay())
		l += fmt.Sprintf("There are %d days remaining in %d<br>", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay()-time.Now().YearDay(), time.Now().Year())
		//		calendar, err := script.Exec(`bash -c 'set -o pipefail ; unbuffer cal --color | lolcat -f -F 0.5'`).String()
		//		if err != nil {
		calendar := cal()
		//		}
		l += "\n" + string(ansihtml.ConvertToHTML([]byte(calendar)))
		l += "\n\n<table style='border-collapse: collapse; width: auto;'>\n"
		l += "\n\n<table style='border-collapse: collapse; width: auto;'>\n"
		l += "<thead>\n"
		l += "<tr>\n"
		l += "<th style='text-align: center;'> <br> <u>RewardDate</u> </th><th style='text-align: center;'> Pool 1 <br> <u>SKY/VISOR</u> </th><th style='text-align: center;'> Pool 2 <br> <u>SKY/VISOR</u> </th><th style='text-align: center;'> Distributed <br> <u>[<span style='color: red;'>&#10060;</span>/<span style='color: green;'>&#10004;</span>]</u> </th>\n"
		l += "</tr>\n"
		l += "</thead>\n"
		l += "<tbody>\n"
		rewardtxncsvs, _ := script.FindFiles(`rewards/hist`).MatchRegexp(regexp.MustCompile(".?.?.?.?-.?.?-.?.?_rewardtxn0.csv")).Replace(wd+`/`+"hist/", "").Replace("_rewardtxn0.csv", "").Slice() //nolint
		for i := len(rewardtxncsvs) - 1; i >= 0; i-- {
			skycoinpershare, _ := script.File(wd+`/`+"hist/"+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share: ").Replace("Skycoin Per Share: ", "").String() //nolint
			skycoinpershare1 := ""
			skycoinpershare2 := ""
			if strings.TrimSpace(skycoinpershare) == "" {
				skycoinpershare1, _ = script.File(wd+`/`+"hist/"+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 1): ").Replace("Skycoin Per Share (Pool 1): ", "").String() //nolint
				skycoinpershare2, _ = script.File(wd+`/`+"hist/"+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 2): ").Replace("Skycoin Per Share (Pool 2): ", "").String() //nolint
				skycoinpershare1 = strings.TrimSpace(skycoinpershare1)
				skycoinpershare2 = strings.TrimSpace(skycoinpershare2)
			} else {
				skycoinpershare1 = strings.TrimSpace(skycoinpershare)
				skycoinpershare2 = ""
			}

			var distributedIcon string
			if _, err := os.Stat(wd + `/` + "hist/" + rewardtxncsvs[i] + ".txt"); err == nil {
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
			Content: htmpl.HTML(l), //nolint
		}
		tmplData := map[string]interface{}{
			"Page": htmlPageTemplateData1,
		}
		var result bytes.Buffer
		err = tmpl.Execute(&result, tmplData)
		if err != nil {
			fmt.Println("error: ", err)
		}

		c.Writer.Write(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1)) //nolint
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
			c.Writer.Write([]byte("len(wlkeys) == 0")) //nolint
			return
		}
		// Read the request body
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("io.ReadAll(c.Request.Body) :\n\n" + string(body) + "\n\nError:\n\n" + err.Error())) //nolint
			return
		}
		//check that wallet is running
		status, err := script.Exec("skycoin-cli status").String()
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("skycoin-cli status:\n\n" + status + "\n\nskycoin-cli status error:\n\n" + err.Error())) //nolint
			return
		}
		//find all transacion csvs
		f, err := script.FindFiles(wd + `/hist/`).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice()
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte(`script.FindFiles(wd + /hist/).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice():\n\n` + strings.Join(f, "\n") + "\n\nError:\n\n" + err.Error())) //nolint
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
					c.Writer.Write([]byte("skycoin-cli decodeRawTransaction:\n\n" + decoded + "\n\nskycoin-cli decodeRawTransaction error:\n\n" + err.Error())) //nolint
					return
				}
				//if all is well, broadcast the transaction
				txid, err := script.Exec("skycoin-cli broadcastTransaction " + string(body)).String()
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte("skycoin-cli broadcastTransaction:\n\n" + txid + "\n\nskycoin-cli broadcastTransaction error:\n\n" + err.Error())) //nolint
					return
				}
				//record the transaction ID for that day's reward
				_, err = script.Echo(txid).WriteFile(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1))
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte(`script.Echo(txid).WriteFile(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1))\n\n` + txid + "\n\n" + strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1) + "\n\nerror:\n\n" + err.Error())) //nolint
					return
				}
				//record the transaction ID for the reward notification system - append the file!
				_, err = script.Echo(txid).AppendFile(wd + `/` + "transactions0.txt")
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte(`script.Echo(txid).AppendFile(wd + / + "transactions0.txt")\n\n` + txid + "\n\nerror:\n\n" + err.Error())) //nolint
					return
				}
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Write([]byte(txid)) //nolint
				return
			}
		}
		c.Writer.WriteHeader(http.StatusNotFound)
		h, _ := script.FindFiles(wd + `/hist/`).String()                      //nolint
		c.Writer.Write([]byte("No undistributed rewards csv found.\n\n" + h)) //nolint
	})

	r1.GET("/skycoin-rewards/csv", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint
		if strings.TrimRight(active, "\n") == "active" {
			c.Writer.Header().Set("Server", "")
			c.Writer.WriteHeader(http.StatusNotFound)
			return
		}
		c.Writer.Header().Set("Server", "")
		f, _ := script.FindFiles(wd + `/hist/`).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).BaseName().Slice() //nolint
		for _, f1 := range f {
			g, err := script.File(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String()
			if err != nil || g == "" || g == "\n" || g == "test" || g == "test\n" {
				c.Writer.Header().Set("Content-Type", "text/plain")
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Write([]byte("skycoin-rewards/hist/" + f1)) //nolint
				return
			}

		}
		c.Writer.WriteHeader(http.StatusNotFound)
	})
	//status of reward system hourly run.
	r1.GET("/skycoin-rewards/s", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint
		c.JSON(http.StatusOK, gin.H{"active": strings.TrimRight(active, "\n")})
	})
	r1.GET("/health", func(c *gin.Context) {
		runTime = time.Since(startTime)
		nextrun, _ := script.Exec(`systemctl status skywire-reward.timer --lines=0`).First(5).Last(1).Replace("    Trigger: ", "").String() //nolint
		prevDuration, _ := script.Exec(`systemctl status skywire-reward.service --lines=0`).Match("Duration").First(1).String()             //nolint
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String()                                                     //nolint
		c.JSON(http.StatusOK, gin.H{
			"frontend_start_time":             startTime,
			"frontend_run_time":               runTime.String(),
			"dmsg_discovery":                  dmsgDisc,
			"dmsg_address":                    fmt.Sprintf("%s:%d", pk.String(), dmsgPort),
			"reward_system_active":            strings.TrimRight(active, "\n"),
			"reward_system_next_run":          strings.TrimRight(nextrun, "\n"),
			"reward_system_prev_run_duration": strings.TrimRight(prevDuration, "\n"),
			"whitelisted_keys":                wlkeys,
		})
	})

	r1.GET("/skycoin-rewards/csv/plain", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint
		if strings.TrimRight(active, "\n") == "active" {
			c.Writer.Header().Set("Server", "")
			c.Writer.WriteHeader(http.StatusNotFound)
			return
		}
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Content-Type", "text/plain")
		f, _ := script.FindFiles(wd + `/hist/`).MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).BaseName().Slice() //nolint
		for _, f1 := range f {
			g, _ := script.File(wd + `/hist/` + strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String() //nolint
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
			if err1 != nil && err2 != nil && err3 != nil && err4 != nil {
				fmt.Println("cant parse date or match filename")
				c.Writer.WriteHeader(http.StatusNotFound)
				c.Writer.Flush()
				return
			}
			if err1 == nil || err2 == nil {
				filetoserve, err := script.File(wd + `/hist/` + c.Param("date")).Bytes()
				if err == nil {
					c.Writer.Header().Set("Content-Type", "text/plain")
					c.Writer.WriteHeader(http.StatusOK)
					c.Writer.Flush()
					c.Writer.Write(filetoserve) //nolint
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
					thispk, _ := script.Echo(line).Column(2).String() //nolint
					reason, _ := script.Echo(line).Column(3).String() //nolint
					toserve += fmt.Sprintf("%s%s\n", strings.TrimRight(strings.TrimRight(thispk, "\n"), "\r"), strings.TrimRight(strings.TrimRight(strings.TrimRight(reason, "\n"), "\r"), ","))
				}
				c.Writer.Header().Set("Content-Type", "text/plain")
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Flush()
				c.Writer.Write([]byte(toserve)) //nolint
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
					thispk, _ := script.Echo(line).Column(2).String() //nolint
					share, _ := script.Echo(line).Column(3).String()  //nolint
					sky, _ := script.Echo(line).Column(4).String()    //nolint
					toserve += fmt.Sprintf("%s%s%s\n", strings.TrimRight(strings.TrimRight(thispk, "\n"), "\r"), strings.TrimRight(strings.TrimRight(share, "\n"), "\r"), strings.TrimRight(strings.TrimRight(strings.TrimRight(sky, "\n"), "\r"), ","))
				}
				c.Writer.Header().Set("Content-Type", "text/plain")
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Flush()
				c.Writer.Write([]byte(toserve)) //nolint
				c.Writer.Flush()
				return
			}

		}
		rewardfiles, _ := script.FindFiles(`rewards/hist`).Match(c.Param("date")).Slice() //nolint
		if len(rewardfiles) == 0 {
			c.Writer.WriteHeader(http.StatusNotFound)
			c.Writer.Flush()
			return
		}
		l := ""
		l3, _ := os.Stat(wd + `/hist/` + c.Param("date") + "_rewardtxn0.csv") //nolint
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
				thispk, _ := script.Echo(line).Column(2).String() //nolint
				share, _ := script.Echo(line).Column(3).String()  //nolint
				sky, _ := script.Echo(line).Column(4).String()    //nolint
				l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + strings.TrimRight(share, "\n") + strings.Replace(sky, ",\n", "\n", -1)
			}
		}
		l2, err = script.File(wd + `/hist/` + c.Param("date") + "_ineligible.csv").Slice()
		if err == nil {
			l += "\n\nIneligible:\n"
			for _, line := range l2 {
				thispk, _ := script.Echo(line).Column(2).String()         //nolint
				reason, _ := script.Echo(line).Column(3).String()         //nolint
				invalid, _ := script.Echo(line).Match(", , , ,").String() //nolint
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

		l1, _ = script.File(wd + `/hist/` + c.Param("date") + "_stats.txt").String() //nolint
		l += c.Param("date") + "_stats.txt\n" + l1 + "\n"

		l2, _ = script.File(wd+`/`+"hist/"+c.Param("date")+"_rewardtxn0.csv").Replace(",", " ").Slice() //nolint
		l += c.Param("date") + "_transaction0.csv\n\nSKY Address, Amount\n"
		for _, line := range l2 {
			skyaddr, _ := script.Echo(line).Column(1).String() //nolint
			skyamt, _ := script.Echo(line).Column(2).String()  //nolint
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
			Content: htmpl.HTML(l), //nolint
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
		c.Writer.Write(result.Bytes()) //nolint
		c.Writer.Flush()
	})

	authRoute.GET("/node-info/:pk", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		//override the behavior of `public fallback` for this endpoint
		if len(wlkeys) == 0 {
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.Writer.Write([]byte("len(wlkeys) == 0")) //nolint
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
		c.Writer.Write(ni) //nolint
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
		rewardtxncsvs, err := script.FindFiles(wd+`/hist`).MatchRegexp(regexp.MustCompile(".?.?.?.?-.?.?-.?.?_rewardtxn0.csv")).Replace(wd+`/hist/`, "").Replace("_rewardtxn0.csv", "").Slice() //nolint
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error #1 " + err.Error())) //nolint
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
			skycoinpershare, err := script.File(wd+`/hist/`+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share: ").Replace("Skycoin Per Share: ", "").String() //nolint
			if err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				c.Writer.Write([]byte("500 Internal Server Error #2 " + err.Error())) //nolint
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			if strings.TrimSpace(skycoinpershare) == "" {
				pool1, err := script.File(wd+`/hist/`+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 1): ").Replace("Skycoin Per Share (Pool 1): ", "").String() //nolint
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte("500 Internal Server Error #3 " + err.Error())) //nolint
					c.AbortWithStatus(http.StatusInternalServerError)
					return
				}
				rdata.One, err = strconv.ParseFloat(strings.TrimRight(pool1, "\n"), 64)
				if err != nil {
					rdata.One = 0.0
				}
				pool2, err := script.File(wd+`/hist/`+rewardtxncsvs[i]+"_stats.txt").Match("Skycoin Per Share (Pool 2): ").Replace("Skycoin Per Share (Pool 2): ", "").String() //nolint
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte("500 Internal Server Error #5 " + err.Error())) //nolint
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
			c.Writer.Write([]byte("500 Internal Server Error" + err.Error())) //nolint
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, txids)
	})

	r1.StaticFile("/log-collection/json", filepath.Join(os.TempDir(), "log-collection.json"))

	faviconBase64 := `AAABAAEAICAAAAEAIACoEAAAFgAAACgAAAAgAAAAQAAAAAEAIAAAAAAAABAAACIuAAAiLgAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAC4BIwAAAAoBnVRgGb5mdEjQcH9d2HeCXdl5gV7Mcnhoy3J3a9B2emjc
	gnxg34d7XsJ1bUGiZGAq0YVyXOGQdV7ikXVd5JJ3XtSKcVy6fWtKmGdmNIBYXhP///8AAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACZVWEAUDg1Ardhc1bRboLA13KF69x1iPXeeIfv3XmF
	6d99hN/gf4Pb4oGD2+WIgd3jiH/p1oN2yK1tZFbIgGyE24h1yeCPdODVim6ty4RtfcOAbmK1dmlq
	xoNpcN6VbXP0pnZy5ptyaceFYh7//+AAAAAAAAAAAAAAAAAAAAAAAJVQXgBRMDMDtl9zXMRne6K8
	ZHWKtmJwgLZjcHS2YHBuumVycMVvdmzJdHRqx3dxZrtvam63cWtoomtkHo5aSQOnYWAcsG1gIalx
	YkrQhnGc65h52PqigOz8pID2/Kh/+v6tfvr8r3n676Zyvd+YbS//140AwoVkAFtKNQBJQysCXU40
	A3c7TwWtV29YxmV+otVribfcb43J4HON2eN2juXedon54HqI/eF+hf3fgYH21358ybBraXKaY1wT
	mFpbAKBiXhPKgG9bwHdtcsl/bWnYi3CA5ZV0rfOfe+L7p374/qt+//+xe//+sXr/8qhzvsuNXyTk
	n2sA/8PzAK9ZdFC7XHx4pVNsOc5kh9LibJT/6HCY/+tzl//td5X/73qV//F+lP/xgJT/5n6K9NJ3
	fK2+bW9qzXl0kcp9cH6ybWUyrm1jS9uJdLrzlIT07pOA3d+Ld7TYiW+ByoJoaM+IZ4Hqm3S39aZ4
	7P6ve//9sHr+6qNuk6pyWgqMQF4ex1mIwNRfkP61VXh7y2GGx+Zqmf/pbJv/6m+Z/+t0l//teJX/
	63qR/tp1hdm4Zm+AwWp1f+J+h9DqhIn+z3l3ndN8eaHbhHq0yX1tcfOUhPP9mYn//puI//qcg/7x
	mXzj55V3scyEa3zPiWlo55pwm/eodeLrom+wtn9fHLJSeknTWJPp312c/8NZhLa1V3eJ3WeU/Ohq
	nP/obJr/6nKX/+R0kf3LbH65rFtrdcxsf7jofI/18YSR/+qDivi1aGmM4IN/w+2LhfnLeXJ33Yd6
	uvmXh//9m4j//56H//+ghv/+oYT/+aF/9+ybd9PXjW6JxoRqU7l8YSi+jmECtEx9bdZXl/3hWqD/
	01uS7aNMbXzNYYjY5WqZ/+Zsmf/ebZD7vmJ3sK1bbHvXb4fU63uS//B+lP/zgZX/5H6I6bNlaXXp
	hIfh946N/9qAfMjFdW568pKE9PyXif/+mon//52H//6ghP/6oIH68Z1649qPcKq9fmZAq3FdCrF5
	XwCvR3x91lOZ/+BWof/bWpr/ulN/rLFUdZrbZZL83GaS+bhaeaWmU2yS1myJ5uZzk//kdY//43iM
	/+Z7jP/UdoDByHN1cumDiPv0io7/64iG87pta33hh33I+ZOK//mVif/xln/t7JV7utSHcIHKgmll
	2YxvgeeWdbvfkXGoxoNiRqxIeG3QUZX831Oh/99WoP/OWI7mpU1uXK9QdYK2WHd4jUpeQKhYbaG+
	X3mdxWR9hMlofnfDaXd4w2x1erdpbUm3bWtByXN2nNZ6fLbhgoHPyXV2g816dEXdhH2b24V6msN3
	a3PEeGt95Y18qvKZfd76oID+/6SE//ykgP/ckXGkp01yQctQkuLbUZ//1lOZ/cBShcWcSGcuAAAA
	AXY4TRG3W3dfvlt+lcZfga3IY4HE0GmFyNZuhsvNbX7ItmZuUMVueIjNb32pyG55j8J1cGCiXmAe
	/6GWAIxeTAi3dWVd3IV81PSUhvj8mof//5+F//+hhP//o4X/+aR7/dySbHeDPFkVu0yGqb5Lic2t
	R3uKnEZraZNLXxwAAAAAl0lkRNFgjPDhZJn/5Wia/+hrmv/pbpn/7HOZ/+Bxj+e2YXBx3naI6Ox+
	kP/gfIfovXBuhJ9iWhx0Rz4Cr29hF7NvY1vNfm+H4453zPWYgvf9m4b//6GD//+ig//xnHnbzodo
	OP+CuACGP1slnkRwcrlJhsi8ToXbm0dpY6tOdH6dSWlqxlqGxeBhmv/mZpz/52ic/+lsnP/pb5n/
	2myMwbVfcYLkdY/23HWGz7lmbnPIb3eR1Hh9xrtrb1nEdG+L4oWA6Nd+erDEeGtwyXxuf+aPfMn3
	moL9+ZyB/N+QdIN+UUQDjENfAFk4NgWqSXeGx0+P+7BHfK6rR3ig01SX/cdUis+jS2x1y12I0+Jl
	mv/lZpv/52qa/+Vsl//KZoGnwWZ4m9Btgc6yYGxqzG5+uOh9jfzwg5D/0nV+sMdydZLxiov/9Y6L
	/+yLhe/bhHmswnZrataGcpLZiHSnv3dnHtKDcQBZITcArk6AAIo7YRalSXVnl0JpeMZRjOfcU6D/
	2lWc/8FUhsebR2iDy1yJ0+Jkmv/mZp3/4WmW+LJccYutXW2XrFtsc85sf8bqeZH+8H2V//GBk//Z
	eYHPynN2ceuEiv33i4//946N//SQiP/ih3/bt3BqR5hfUwySX04BmWFTAAAAAAAAAAAAOE8UAKBE
	cACZRGsvtkqBscpOkufSUpfy0laU/7tSgc6eS2h6yFyHxd5imP/YZpDroFJpUpFPX0TIZn+85XSR
	/+13l//vepb/8H2U/996hua1ZWt35oGI7u+Hi/bpiITn3oR8utB8dme0a2kUx3dyAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAJgAgAA4AEAGFOVsNnD1wKaFDcVC9S4fJ2FSa/8NTh9iiSW2Bull6mrtdenyN
	SVwNwWF+feBrk/vrcZn/7HWY/+13lv/vepb/5nqN6rdlbXPUd33Vw210Z8RxcSiybGUQb0ZAA3ZK
	RAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACNOWIAAAAAArtJh4DZU53/3Fad
	/81WkN+fS2pTAAAAAQAAAAHKZISe5GqY/+htmf/qcZf/7HSX/+54l//oeZDtuGdveMNvc7/Fb3Qa
	yHF2AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAFsbQgDi
	YqIArUZ8VNBRlfXeVaD/2leb/7lTf5d6PFIHFxkDA7hedWPUZI3f42uW/+lvmf/rcpj/7XWY/+Z2
	kfOyYm6BrGVoeqtkZxCtZmkAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAJZDZgCRQmIdwk2LwtpPn//cU5//yFOMyJ9PZ0rDW4KavFl8lKVSa3e6XHig
	0GaH4eFskvrocZb/23GK8aZeaGaJVlgdhFFUAohUVwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAEHAP+B4wCqSXZWzE6T7tdTmv+7T4KutU58odpa
	mP/cXpn8zl2L5bZVebGiTmmJsFhxkcxpga+2YHGKg0dRFJxTYAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAo0hvAIA/Uwa1TH5+
	xVGL6KVIcYzGU4rJ3lme/+Jdn//jYJ3/4GKZ/9Zij+/OY4e8sVpyi4tNWCWNTloAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAizxeAIo7XQyaSWpRoUhwWctTkODcVp3/3lid/+Fenf/iYJz/4GSZ/9djkPC8XHuckVFc
	HJVSXwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKRKcgCjSnEVrkp5c71OhLvCUojcyFWL3cZahsTB
	XX+QuVl5S49GXA6rVG8AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAG4zSAAvFh0Dj0Jg
	D5NAZBuYQWccnlBmEYBMSwaSUVsAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
	AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA////
	//////////////////AAAH/gAAAH4AAAA4AAQAGAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAQA
	AgAAAIAAAACAAAABwAAAAfAAAAfwAAAP/gAAf/8AAH//AAB//4AB//+AA///wAP///AH///4H///
	//////////////8=`
	faviconBuffer, _ := base64.StdEncoding.DecodeString(faviconBase64) //nolint

	r1.GET("/favicon.ico", func(c *gin.Context) {
		_, _ = c.Writer.WriteString(string(faviconBuffer)) //nolint
	})

	//manually create routes to the compiled cogentcore web app source files
	filepath.Walk(outputDir+"/bin/web", func(path string, info os.FileInfo, err error) error { //nolint
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

	// Start the server using the custom Gin handler
	serve := &http.Server{
		Handler:           &ginHandler{Router: r1},
		ReadHeaderTimeout: 3 * time.Second,
	}

	wg := new(sync.WaitGroup)
	// Start serving
	wg.Add(1)
	go func() {
		fmt.Printf("listening on http://127.0.0.1:%d using gin router\n", webPort)
		r1.Run(fmt.Sprintf(":%d", webPort)) //nolint
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
		for range time.NewTicker(cacheInterval).C {
			err := generateAndCacheJSON()
			if err != nil {
				fmt.Println("Error updating log-collection cache:", err)
			}
		}
	}()

	wg.Wait()
}

var (
	tempJSONPath  = filepath.Join(os.TempDir(), "log-collection.json")
	cacheInterval = 300 * time.Second // refresh cache every 5 minutes seconds
)

// Node represents a single node's flattened information.
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

func generateAndCacheJSON() error {
	pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
	if err != nil {
		return err
	}

	var nodes []node
	for i := range pks {
		healthPath := wd + "/log_backups/" + pks[i] + "/health.json"
		nodeInfoPath := wd + "/log_backups/" + pks[i] + "/node-info.json"

		fileInfo, err := os.Stat(healthPath)
		if err != nil {
			continue
		}
		_, err = os.Stat(nodeInfoPath)
		if err != nil {
			continue
		}

		modTime := fileInfo.ModTime().Format(time.RFC3339)

		healthData, err := script.File(healthPath).Bytes()
		if err != nil {
			continue
		}

		var temp struct {
			BuildInfo struct {
				Version string `json:"version"`
				Commit  string `json:"commit"`
				Date    string `json:"date"`
			} `json:"build_info"`
			StartedAt string `json:"started_at"`
		}

		if err := json.Unmarshal(healthData, &temp); err != nil {
			continue
		}

		nodeInfoSlc, err := script.File(nodeInfoPath).JQ(".skywire_version").Replace(`"`, "").Replace("\n", "").Slice()
		if err != nil || len(nodeInfoSlc) == 0 {
			continue
		}

		nodes = append(nodes, node{
			PK:        pks[i],
			Time:      modTime,
			Version:   nodeInfoSlc[0],
			Commit:    temp.BuildInfo.Commit,
			Date:      temp.BuildInfo.Date,
			StartedAt: temp.StartedAt,
		})
	}

	data := nodesResponse{Nodes: nodes}
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(data); err != nil {
		return err
	}

	return os.WriteFile(tempJSONPath, buf.Bytes(), 0644) //nolint
}

type ginHandler struct {
	Router *gin.Engine
}

func (h *ginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Router.ServeHTTP(w, r)
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		if latency > time.Minute {
			latency = latency.Truncate(time.Second)
		}
		reqHost := c.Request.Host

		fmt.Printf("[FIBER] %s |%s %3d %s| %13v | %15s | %72s | %18s |%s %-7s %s %s\n",
			time.Now().Format("2006/01/02 - 15:04:05"),
			getBackgroundColor(c.Writer.Status()),
			c.Writer.Status(),
			resetColor(),
			latency,
			c.ClientIP(),
			c.Request.RemoteAddr,
			reqHost,
			getMethodColor(c.Request.Method),
			c.Request.Method,
			resetColor(),
			c.Request.URL.Path,
		)
	}
}
func getBackgroundColor(statusCode int) string {
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return green
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return white
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return yellow
	default:
		return red
	}
}

func getMethodColor(method string) string {
	switch method {
	case http.MethodGet:
		return blue
	case http.MethodPost:
		return cyan
	case http.MethodPut:
		return yellow
	case http.MethodDelete:
		return red
	case http.MethodPatch:
		return green
	case http.MethodHead:
		return magenta
	case http.MethodOptions:
		return white
	default:
		return reset
	}
}

func resetColor() string {
	return reset
}

type consoleColorModeValue int //nolint

var consoleColorMode = autoColor //nolint

const (
	autoColor    consoleColorModeValue = iota //nolint
	disableColor                              //nolint
	forceColor                                //nolint
)

const (
	green   = "\033[97;42m"
	white   = "\033[90;47m"
	yellow  = "\033[90;43m"
	red     = "\033[97;41m"
	blue    = "\033[97;44m"
	magenta = "\033[97;45m"
	cyan    = "\033[97;46m"
	reset   = "\033[0m"
)

var (
	// html snippets
	nl          []string
	navlinks    string
	htmltoplink = "<a href='#top'>top of page</a>\n"
	htmlend     = "</pre></body></html>"
)

func init() {
	nl = append(nl, "  <a href='/'>fiber</a>")
	nl = append(nl, "  <a href='/skycoin-rewards'>skycoin rewards</a>")
	nl = append(nl, "  <a href='/log-collection'>log collection</a>")
	nl = append(nl, "  <a href='/log-collection/tree'>survey index</a>")
	nl = append(nl, "  <a href='/log-collection/tplogs'>transport logging</a>")
	nl = append(nl, "  <a href='"+strings.ReplaceAll(skywire.Prod.UptimeTracker, "http://", "https://")+"/uptimes?v=v2'>uptime tracker</a>")
	nl = append(nl, "  <a href='"+strings.ReplaceAll(skywire.Prod.AddressResolver, "http://", "https://")+"'>address resolver</a>")
	nl = append(nl, "  <a href='"+strings.ReplaceAll(skywire.Prod.TransportDiscovery, "http://", "https://")+"/all-transports'>transport discovery</a>")
	nl = append(nl, "  <a href='"+strings.ReplaceAll(skywire.Prod.DmsgDiscovery, "http://", "https://")+"/dmsg-discovery/entries'>dmsgd entries</a>")
	nl = append(nl, "  <a href='"+strings.ReplaceAll(skywire.Prod.DmsgDiscovery, "http://", "https://")+"/dmsg-discovery/all_servers'>all dmsg servers</a>")
	nl = append(nl, "  <a href='"+strings.ReplaceAll(skywire.Prod.DmsgDiscovery, "http://", "https://")+"/dmsg-discovery/available_servers'>available dmsg servers</a>")
	nl = append(nl, "\n<br>\n")
	navlinks = strings.Join(nl, "")

}

func scriptExecString(s string) string {
	if runtime.GOOS == "windows" {
		var variable, defaultvalue string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
			defaultvalue = strings.TrimRight(parts[1], "}")
		} else {
			variable = s
			defaultvalue = ""
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return defaultvalue
			}
			return strings.TrimRight(out, "\n")
		}
		return defaultvalue
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		return strings.TrimSpace(z)
	}
	return ""
}

func scriptExecArray(s string) string {
	if runtime.GOOS == "windows" {
		variable := s
		if strings.Contains(variable, "[@]}") {
			variable = strings.TrimRight(variable, "[@]}")
			variable = strings.TrimRight(variable, "{")
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; foreach ($item in %s) { Write-Host $item }'`, skyenvfile, variable)).Slice()
		if err == nil {
			if len(out) != 0 {
				return ""
			}
			return strings.Join(out, ",")
		}
	}
	y, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`, skyenvfile, s)).Slice()
	if err == nil {
		return strings.Join(y, ",")
	}
	return ""
}

func scriptExecInt(s string) int {
	if runtime.GOOS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return i
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return i
		}
	}
	return 0
}
func scriptExecUint(s string) uint {
	if runtime.GOOS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return uint(i) //nolint: gosec
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return uint(i) //nolint: gosec
		}
	}
	return uint(0)
}

func scriptExecUint16(s string) uint16 {
	if runtime.GOOS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				if i >= 0 && i <= 65535 {
					return uint16(i) //nolint
				}
				return 0
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			if i >= 0 && i <= 65535 {
				return uint16(i) //nolint
			}
			return 0
		}
	}
	return uint16(0)
}

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error")) //nolint
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
			c.Writer.Write([]byte("401 Unauthorized")) //nolint
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}
}

type htmlTemplateData struct {
	Title   string
	Page    string
	Content htmpl.HTML
}

const htmlFrontPageTemplate = `
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬─┐┌─┐┬ ┬┌─┐┬─┐┌┬┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤   ├┬┘├┤ │││├─┤├┬┘ ││└─┐
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  ┴└─└─┘└┴┘┴ ┴┴└──┴┘└─┘<br>
{{.Page.Content}}
`

func mainPage(c *gin.Context) {
	c.Writer.Header().Set("Server", "")
	tmpl0, err1 := tmpl.Clone()
	if err1 != nil {
		fmt.Println("Error cloning template:", err1)
	}
	_, err1 = tmpl0.New("this").Parse(htmlFrontPageTemplate)
	if err1 != nil {
		fmt.Println("Error parsing Front Page template:", err1)
	}
	tmpl := tmpl0

	mainnetRulesHtml, _ := script.Exec(`skywire cli reward rules -l`).String()              //nolint
	skywireVersion, _ := script.Exec(`skywire -v`).Replace("skywire version ", "").String() //nolint
	htmlPageTemplateData1 := htmlPageTemplateData
	htmlPageTemplateData1.Content = htmpl.HTML(skywireVersion + "<br>" + skycoinlogohtml + "<br>" + mainnetRulesHtml) //nolint
	tmplData := map[string]interface{}{
		"Page": htmlPageTemplateData1,
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, tmplData)
	if err != nil {
		fmt.Println("error: ", err)
		c.Writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write((bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1))) //nolint
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

const skycoinlogohtml = `<table border="0" cellpadding="0" cellspacing="0" summary="[libcaca canvas export]">
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">:</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#aaaaaa" colspan="2"><tt><font color="#555555">St</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa" colspan="2"><tt><font color="#555555">;</font><font color="#ffffff">.</font></tt></td><td bgcolor="#aaaaaa" colspan="3"><tt><font color="#555555">&#160;%8</font></tt></td><td bgcolor="#555555" colspan="2"><tt><font color="#00aaaa">&#160;;</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">@</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">%.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;@</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">;</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">.</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa5500">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#0000aa">%</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.X</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#aa0000">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#00aa00">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;S</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">;</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">:</font><font color="#aa0000">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">:</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;S</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">S</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">X</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">:</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#00aa00">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">:</font><font color="#0000aa">.</font><font color="#aa0000">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">t</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#0000aa">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.;</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#555555">8</font><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;X</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;..</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">t</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">S.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">.</font><font color="#aa0000">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#aa0000">.</font><font color="#0000aa">.;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">;</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">X</font></tt></td><td bgcolor="#555555" colspan="2"><tt><font color="#000000">X</font><font color="#00aaaa">.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;S</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#000000" colspan="4"><tt><font color="#00aa00">t</font><font color="#aa0000">:</font><font color="#0000aa">.</font><font color="#00aa00">;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">X</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#aaaaaa" colspan="2"><tt><font color="#555555">:</font><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">;</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">.</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">@</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#aa0000">.</font><font color="#0000aa">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;S</font></tt></td><td bgcolor="#aaaaaa" colspan="2"><tt><font color="#ffffff">;</font><font color="#555555">8</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">:</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">S</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">8</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="4"><tt><font color="#aaaaaa">&#160;..:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#00aa00">;</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">:</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;X</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">:</font><font color="#0000aa">.</font><font color="#aa0000">:</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">8.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">8</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa0000">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">:t</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">:</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;X</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#00aa00">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.%</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">X</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">S</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">:</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">:</font><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">:</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">:</font><font color="#00aa00">.</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.t</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#aa0000">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">.</font><font color="#aa0000">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#0000aa">:</font><font color="#00aa00">.</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.@</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#0000aa">;</font><font color="#00aa00">:</font><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">:</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#555555">8</font><font color="#00aa00">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;..</font></tt></td><td bgcolor="#555555"><tt><font color="#aa5500">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;S</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa5500">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">:</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;</font><font color="#0000aa">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">%</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">;</font><font color="#0000aa">:</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">8</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">%</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">%</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">;</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">.</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;</font><font color="#555555">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#555555">8</font><font color="#aa0000">..</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">.</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa5500">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">t</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">:</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;@</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">;</font><font color="#aa0000">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">..</font><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">:</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">8</font><font color="#0000aa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#0000aa">..</font><font color="#aa0000">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">@</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa0000">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">8</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;..</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">;</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">.</font><font color="#aa0000">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;@</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;&#160;t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">:</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">:</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#aa0000">:</font><font color="#0000aa">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">%</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;X</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#00aa00">.:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.8</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">:</font><font color="#aa0000">.</font><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">8</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;..</font></tt></td><td bgcolor="#555555"><tt><font color="#aa5500">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">;</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">t</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">%</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">@</font></tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#00aa00">;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">:.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">@</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">@</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000" colspan="3"><tt><font color="#0000aa">&#160;</font><font color="#aa0000">&#160;.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">@</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#00aa00">:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#0000aa">:</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.t</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">8</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">:</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">%</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">.</font><font color="#aa0000">t</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">X.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">.</font><font color="#aa0000">.</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#aa0000">:</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.t</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#0000aa">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;S</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#555555">S</font><font color="#0000aa">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">S.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#555555">@</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">t.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.8</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">;</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">@</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">t</font></tt></td><td bgcolor="#000000"><tt><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#555555">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">X</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;;</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;..</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa5500">.</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">S</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">t</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;.S</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">X</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">:</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#0000aa">:</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">X</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#00aa00">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">X</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">;</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;%</font></tt></td><td bgcolor="#000000"><tt><font color="#555555">@</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;:</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">%</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt><font color="#aa0000">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">X</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;X</font></tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">;</font><font color="#aa0000">:</font><font color="#0000aa">;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">8.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;t</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000"><tt><font color="#0000aa">.</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;t</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">%</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">t</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">:</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#555555"><tt><font color="#00aaaa">;</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555" colspan="2"><tt><font color="#000000">8</font><font color="#aaaaaa">8</font></tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#00aa00">:</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;@</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#555555">S</font><font color="#00aa00">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="3"><tt><font color="#aaaaaa">&#160;..</font></tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;</font><font color="#0000aa">.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">%</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">%</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;</font><font color="#555555">X</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">8</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">;</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#000000"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#555555"><tt>&#160;</tt></td><td bgcolor="#aaaaaa" colspan="2"><tt><font color="#555555">8</font><font color="#ffffff">:</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">8;</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#000000">%</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aa00aa">:</font></tt></td><td bgcolor="#ffffff"><tt><font color="#aaaaaa">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#555555"><tt><font color="#aaaaaa">S</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">S</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#555555">8</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">t</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#aaaaaa"><tt><font color="#ffffff">.</font></tt></td><td bgcolor="#ffffff"><tt>&#160;</tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;.</font></tt></td><td bgcolor="#ffffff" colspan="2"><tt><font color="#aaaaaa">&#160;8</font></tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="5"><tt><font color="#aa0000">&#160;t</font><font color="#0000aa">:</font><font color="#00aa00">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#aa0000">&#160;.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="6"><tt><font color="#aa0000">&#160;;</font><font color="#555555">8888</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#00aa00">&#160;t</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000" colspan="7"><tt><font color="#555555">8888888</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">@</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="4"><tt><font color="#00aa00">&#160;</font><font color="#555555">888</font></tt></td><td bgcolor="#555555"><tt><font color="#000000">8</font></tt></td><td bgcolor="#000000" colspan="6"><tt><font color="#555555">88888</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="11"><tt><font color="#aa0000">&#160;</font><font color="#555555">88888888</font><font color="#00aa00">:</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="12"><tt><font color="#0000aa">&#160;.</font><font color="#555555">8888888</font><font color="#00aa00">;</font><font color="#aa0000">:</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="6"><tt><font color="#aa0000">&#160;</font><font color="#0000aa">.</font><font color="#00aa00">:.</font><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
<tr><td bgcolor="#000000"><tt>&#160;&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="5"><tt><font color="#aa0000">&#160;</font><font color="#00aa00">.</font><font color="#0000aa">.</font><font color="#00aa00">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="3"><tt><font color="#00aa00">&#160;</font><font color="#0000aa">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="4"><tt><font color="#0000aa">&#160;.</font><font color="#00aa00">.</font><font color="#aa0000">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="6"><tt><font color="#aa0000">&#160;.</font><font color="#00aa00">.</font><font color="#aa0000">.</font><font color="#0000aa">.</font><font color="#00aa00">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="9"><tt><font color="#00aa00">&#160;.</font><font color="#0000aa">.</font><font color="#aa0000">.</font><font color="#00aa00">.</font><font color="#aa0000">.</font><font color="#0000aa">.</font><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="8"><tt><font color="#00aa00">&#160;.</font><font color="#0000aa">.</font><font color="#aa0000">.</font><font color="#00aa00">.</font><font color="#0000aa">.</font><font color="#aa0000">.</font><font color="#0000aa">.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000" colspan="2"><tt><font color="#0000aa">&#160;.</font></tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;</tt></td><td bgcolor="#000000"><tt>&#160;&#160;</tt></td></tr>
</table>`
