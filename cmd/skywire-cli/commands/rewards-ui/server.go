// Package clirewardsui cmd/skywire-cli/commands/rewards/ui.go
package clirewardsui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	htmpl "html/template"
	"io"
	"io/fs"
	"log"
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
	RootCmd.CompletionOptions.DisableDefaultCmd = true
	RootCmd.Flags().UintVarP(&webPort, "port", "p", scriptExecUint("${WEBPORT:-80}"), "port to serve")
	RootCmd.Flags().Uint16VarP(&dmsgPort, "dport", "d", scriptExecUint16("${DMSGPORT:-80}"), "dmsg port to serve")
	RootCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", scriptExecInt("${DMSGSESSIONS:-1}"), "dmsg sessions")
	msg := "add whitelist keys, comma separated to permit POST of reward transaction to be broadcast"
	if scriptExecArray("${REWARDPKS[@]}") != "" {
		msg += "\n\r"
	}
	RootCmd.Flags().StringVarP(&wl, "wl", "w", scriptExecArray("${REWARDPKS[@]}"), msg)
	wd, err = os.Getwd()
	if err != nil {
		log.Fatal("Error getting current directory:", err)
	}
	RootCmd.Flags().StringVarP(&wd, "wd", "W", wd, "location of dir containing 'log_collection' & reward 'hist' dirs")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", skywire.Prod.DmsgDiscovery, "dmsg discovery url")
	RootCmd.Flags().StringVarP(&ensureOnlineURL, "ensure-online", "O", scriptExecString("${ENSUREONLINE}"), "Exit when the specified URL cannot be fetched;\ni.e. https://fiber.skywire.dev")
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint
	}
	if scriptExecString("${DMSGHTTP_SK}") != "" {
		sk.Set(scriptExecString("${DMSGHTTP_SK}")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
}

// RootCmd starts the reward system ui server
var RootCmd = &cobra.Command{
	Use:   "rewards-ui",
	Short: "reward system user interface",
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

	//authroute uses the whitelist to control what dmsg clients may connect to authRoute endpoints
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
		f, err := script.FindFiles("rewards/hist").MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice()
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte(`script.FindFiles("rewards/hist").MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice():\n\n` + strings.Join(f, "\n") + "\n\nError:\n\n" + err.Error())) //nolint
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
				_, err = script.Echo(txid).AppendFile("rewards/transactions0.txt")
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte(`script.Echo(txid).AppendFile("rewards/transactions0.txt")\n\n` + txid + "\n\nerror:\n\n" + err.Error())) //nolint
					return
				}
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Write([]byte(txid)) //nolint
				return
			}
		}
		c.Writer.WriteHeader(http.StatusNotFound)
		h, _ := script.FindFiles("rewards/hist").String()                     //nolint
		c.Writer.Write([]byte("No undistributed rewards csv found.\n\n" + h)) //nolint
	})

	authRoute.GET("/skycoinrewards/hist/:date", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		//override the behavior of `public fallback` for this endpoint
		if len(wlkeys) == 0 {
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.Writer.Write([]byte("len(wlkeys) == 0")) //nolint
			return
		}
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		_, err := time.Parse("2006-01-02", c.Param("date"))
		if err != nil {
			if strings.Contains(c.Param("date"), "_rewardtxn0.csv") {
				filetoserve, err := script.File("rewards/hist/" + c.Param("date")).Bytes()
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
		}
		rewardfiles, _ := script.FindFiles(`rewards/hist`).Match(c.Param("date")).Slice() //nolint
		if len(rewardfiles) == 0 {
			c.Writer.WriteHeader(http.StatusNotFound)
			c.Writer.Flush()
			return
		}
		l := ""
		l1, _ := script.File("rewards/hist/" + c.Param("date") + "_stats.txt").String() //nolint
		l += c.Param("date") + "_stats.txt\n" + l1 + "\n"
		l1, err = script.File("rewards/hist/" + c.Param("date") + ".txt").String()
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
		l1, err = script.File("rewards/hist/"+c.Param("date")+"_shares.csv").Replace("[", "&lsqb;").Replace("]", "&rsqb;").Replace("{", "&lcub;").Replace("}", "&rcub;").Replace(":", "&colon;").String()
		if err != nil {
			l += "<div style='float: right;'>PK,Share,SKY Amount\nReward shares file not found\nerror: " + err.Error() + "\n\n"
			l += "</div>"
		} else {
			l += l1
		}
		l1, err = script.File("rewards/hist/" + c.Param("date") + "_ineligible.csv").String()
		if err == nil {
			l += "\n\nIneligible:\n" + l1
		}

		l2, _ := script.File("rewards/hist/"+c.Param("date")+"_rewardtxn0.csv").Replace(",", " ").Slice() //nolint
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
		ni, err := script.File("rewards/log_backups/" + c.Param("pk") + "/node-info.json").Bytes()
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

	//status of reward system hourly run.
	r1.GET("/skycoin-rewards/s", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint
		c.JSON(http.StatusOK, gin.H{"active": strings.TrimRight(active, "\n")})
	})

	r1.GET("/skycoin-rewards", func(c *gin.Context) {
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

	// BuildInfo represents build metadata.
	type buildInfo struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}

	// Health represents the health.json data.
	type health struct {
		Time      string    `json:"time"`
		BuildInfo buildInfo `json:"build_info"`
		StartedAt string    `json:"started_at"`
	}

	// Node represents a single node's information.
	type node struct {
		PK       string `json:"pk"`
		Health   health `json:"health,omitempty"`
		NodeInfo string `json:"node_info,omitempty"`
	}

	type nodesResponse struct {
		Nodes []node `json:"nodes"`
	}

	r1.GET("/log-collection/json", func(c *gin.Context) {
		pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error" + err.Error())) //nolint
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		var nodes []node

		for i := range pks {
			fileInfo, err := os.Stat(wd + "/log_backups/" + pks[i] + "/health.json")
			if err != nil {
				continue
			}
			_, err = os.Stat(wd + "/log_backups/" + pks[i] + "/node-info.json")
			if err != nil {
				continue
			}
			// Get modification time
			modTime := fileInfo.ModTime().Format(time.RFC3339)
			healthData, err := script.File(wd + "/log_backups/" + pks[i] + "/health.json").Bytes()
			if err != nil {
				continue
			}
			var healthJSON health
			err = json.Unmarshal(healthData, &healthJSON)
			if err != nil {
				continue
			}
			healthJSON.Time = modTime

			var nodeInfo string

			nodeInfoSlc, err := script.File(wd+"/log_backups/"+pks[i]+"/node-info.json").JQ(".skywire_version").Replace(`"`, "").Replace("\n", "").Slice()
			if err != nil {
				continue
			}
			if len(nodeInfoSlc) == 0 {
				continue
			}
			nodeInfo = nodeInfoSlc[0]

			nodes = append(nodes, node{
				PK:       pks[i],
				Health:   healthJSON,
				NodeInfo: nodeInfo,
			})
		}

		response := nodesResponse{Nodes: nodes}

		// Set header and return JSON response
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, response)
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
		l3, _ := os.Stat("rewards/hist/" + c.Param("date") + "_rewardtxn0.csv") //nolint
		l += "Reward data generated: " + l3.ModTime().Format("2006-01-02 15:04:05") + "\n\n"

		l1, err := script.File("rewards/hist/" + c.Param("date") + ".txt").String()
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

		l2, err := script.File("rewards/hist/" + c.Param("date") + "_shares.csv").Slice()
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
		l2, err = script.File("rewards/hist/" + c.Param("date") + "_ineligible.csv").Slice()
		if err == nil {
			l += "\n\nIneligible:\n"
			for _, line := range l2 {
				thispk, _ := script.Echo(line).Column(2).String()         //nolint
				reason, _ := script.Echo(line).Column(3).String()         //nolint
				invalid, _ := script.Echo(line).Match(", , , ,").String() //nolint
				if invalid != "" {
					_, err = script.IfExists("rewards/log_backups/" + thispk + "/node-info.json").Echo("").String()
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

		l1, _ = script.File("rewards/hist/" + c.Param("date") + "_stats.txt").String() //nolint
		l += c.Param("date") + "_stats.txt\n" + l1 + "\n"

		l2, _ = script.File("rewards/hist/"+c.Param("date")+"_rewardtxn0.csv").Replace(",", " ").Slice() //nolint
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
		initialLineCount, _ := script.File("rewards/skywire-cli-log.txt").CountLines() //nolint
		// Read and print the initial lines
		initialContent, _ := script.File("rewards/skywire-cli-log.txt").First(initialLineCount).Bytes() //nolint
		c.Writer.Write(ansihtml.ConvertToHTML(initialContent))                                          //nolint
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
			currentLineCount, _ := script.File("rewards/skywire-cli-log.txt").CountLines() //nolint
			// Check if there are new lines
			if currentLineCount > initialLineCount {
				newContent, _ := script.File("rewards/skywire-cli-log.txt").Last(currentLineCount - initialLineCount).Bytes() //nolint
				initialLineCount = currentLineCount
				c.Writer.Write(ansihtml.ConvertToHTML(newContent)) //nolint
				c.Writer.Flush()
			}
			finished, _ := script.File("rewards/skywire-cli-log.txt").Last(1).MatchRegexp(regexp.MustCompile(".*finished.*")).String() //nolint
			if finished != "" {
				break
			}
		}

		c.Writer.Write([]byte(htmltoplink)) //nolint
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend)) //nolint
		c.Writer.Flush()
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

	wg.Wait()
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
