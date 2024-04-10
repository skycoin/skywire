// Package clirewards cmd/skywire-cli/commands/rewards/ui.go
package clirewards

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	htmpl "html/template"
	"io"
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
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/pterm/pterm"
	"github.com/robert-nix/ansihtml"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
)

var x = os.Args[0]

func main() {
	Execute()
}

func init() {
	RootCmd.CompletionOptions.DisableDefaultCmd = true
	RootCmd.AddCommand(
		uiCmd,
	)
	uiCmd.AddCommand(
		runCmd,
		stCmd,
	)
	var helpflag bool
	uiCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var uiCmd = &cobra.Command{
	Use:   "fiber",
	Short: "skycoin reward system and skywire network metrics",
	Long: `
	┌─┐┬┌┐ ┌─┐┬─┐
	├┤ │├┴┐├┤ ├┬┘
	└  ┴└─┘└─┘┴└─
	` + "skycoin reward system and skywire network metrics\nfiber.skywire.dev\n" + x,
}

var pubKey string

func init() {
	stCmd.Flags().StringVarP(&pubKey, "pk", "p", "", "public key to check")
}

var stCmd = &cobra.Command{
	Use:   "st",
	Short: "survey tree",
	Run: func(_ *cobra.Command, _ []string) {
		makeTree()
	},
}

var (
	webPort1 int
	whom     bool
)

func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       rootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

const scriptfile = "fr.sh"

var (
	startTime       = time.Now()
	runTime         time.Duration
	sk              cipher.SecKey
	pk              cipher.PubKey
	dmsgDisc        string
	dmsgPort        uint
	dmsgSess        int
	wl              string
	wlkeys          []cipher.PubKey
	webPort         uint
	logtxtdate      string
	ensureOnlineURL string
)

var skyenvfile = os.Getenv("SKYENV")

func init() {
	runCmd.Flags().UintVarP(&webPort, "port", "p", scriptExecUint("${WEBPORT:-80}"), "port to serve")
	runCmd.Flags().UintVarP(&dmsgPort, "dport", "d", scriptExecUint("${DMSGPORT:-80}"), "dmsg port to serve")
	runCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", scriptExecInt("${DMSGSESSIONS:-1}"), "dmsg sessions")
	msg := "add whitelist keys, comma separated to permit POST `/reward` transaction to be broadcast"
	if scriptExecArray("${REWARDPKS[@]}") != "" {
		msg += "\n\r"
	}
	runCmd.Flags().StringVarP(&wl, "wl", "w", scriptExecArray("${REWARDPKS[@]}"), msg)
	runCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", "", "dmsg discovery url default:\n"+skyenv.DmsgDiscAddr)
	runCmd.Flags().StringVarP(&ensureOnlineURL, "ensure-online", "O", scriptExecString("${ENSUREONLINE}"), "Exit when the specified URL cannot be fetched;\ni.e. https://fiber.skywire.dev\n")
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint
	}
	if scriptExecString("${DMSGHTTP_SK}") != "" {
		sk.Set(scriptExecString("${DMSGHTTP_SK}")) //nolint
	}
	pk, _ = sk.PubKey()
	runCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")

}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run the web application",
	Long: func() string {
		if _, err := os.Stat(skyenvfile); err == nil {
			return `run the web application

	skyenv file detected: ` + skyenvfile
		}
		return `run the web application

	.conf file may also be specified with
	SKYENV=/path/to/fiber.conf fiber run`
	}(),
	Run: func(_ *cobra.Command, _ []string) {
		Server()
	},
}

func makeTree() {
	var tree pterm.TreeNode
	rootDir := "rewards/log_backups/" + pubKey
	if pubKey == "" {
		tree = pterm.TreeNode{
			Text:     "Index",
			Children: getDirNodes(rootDir),
		}
	} else {
		tree = pterm.TreeNode{
			Text:     pterm.Cyan(pubKey),
			Children: getDirChildren(rootDir),
		}
	}
	pterm.DefaultTree.WithRoot(tree).Render()
}

func getDirNodes(dirPath string) []pterm.TreeNode {
	nodes := []pterm.TreeNode{}

	files, err := os.ReadDir(dirPath)
	if err != nil {
		fmt.Printf("Not found\n")
		return nodes
	}

	for _, file := range files {
		if file.IsDir() {
			dirName := file.Name()
			nodes = append(nodes, pterm.TreeNode{
				Text:     pterm.Cyan(dirName),
				Children: getDirChildren(filepath.Join(dirPath, dirName)),
			})
		}
	}

	return nodes
}

func getDirChildren(dirPath string) []pterm.TreeNode {
	nodes := []pterm.TreeNode{}

	files, err := os.ReadDir(dirPath)
	if err != nil {
		fmt.Printf("Not found\n")
		return nodes
	}

	for _, file := range files {
		fileName := file.Name()
		if file.IsDir() {
			nodes = append(nodes, pterm.TreeNode{
				Text:     pterm.Cyan(fileName),
				Children: getDirChildren(filepath.Join(dirPath, fileName)),
			})
		} else if fileName == "health.json" {
			fileContents, err := os.ReadFile(filepath.Join(dirPath, fileName))
			if err != nil {
				fmt.Printf("Error reading file\n")
				continue
			}
			// Get file information
			fileInfo, err := os.Stat(filepath.Join(dirPath, fileName))
			if err != nil {
				fmt.Printf("Error stat-ing file\n")
				continue
			}
			var coloredFile string
			if time.Since(fileInfo.ModTime()) < time.Hour {
				coloredFile = pterm.Green(fileName)
			} else {
				coloredFile = pterm.Red(fileName)
			}
			nodes = append(nodes, pterm.TreeNode{
				Text: fmt.Sprintf("%s Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()), string(fileContents)),
			})
		} else if fileName == "node-info.json" {
			nodes = append(nodes, pterm.TreeNode{
				Text: pterm.Blue(fileName),
			})
		} else {
			nodes = append(nodes, pterm.TreeNode{
				Text: pterm.Yellow(fileName),
			})
		}
	}

	return nodes
}

func tploghtmlfunc() {
	l := "<!doctype html><html lang=en><head><title>Skywire Transport Bandwidth Logs By Day</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"
	l += navlinks
	l += fmt.Sprintf("page updated: %s\n",
		sh("date"))
	l += "<p style='color:blue'>Blue = Verified Bandwidth</p>"
	l += "<p style='color:yellow'>Yellow = Transport bandwidth inconsistent</p>"
	l += "<p style='color:red'>Red = Error: sent or recieved is zero</p>"
	l += fmt.Sprintf("%s\n",
		shh("_tplogshtml"))
	l += htmltoplink
	l += htmlend
	tploghtml = &l
}

func rewardshisthtmlfunc(d string) string {
	l := "<!doctype html><html lang=en><head><title>Rewards for " + d + "</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"
	l += navlinks
	if _, err := os.Stat(fmt.Sprintf("rewards/hist/%s.txt", d)); os.IsNotExist(err) {
		l += "no data for date: " + d + "\n"
	} else {

		l += fmt.Sprintf("<div style='float: right;'>\nreward distribution transaction for %s \n<a href='https://explorer.skycoin.com/app/transaction/%s'>%s</a>\n%s\n</div>",
			d,
			shh(" _txidfromdate "+d),
			shh(" _txidfromdate "+d),
			shh(" _csvcheckdate "+d))
		l += fmt.Sprintf("\nreward distribution CSV for %s\n\n%s\n",
			d,
			shh(" _localcsvcheckdate "+d))
		l += htmltoplink
	}
	l += htmlend
	return l
}

func transportstatshtml() string {
	l := "<!doctype html><html lang=en><head><title>Skywire Transport Statistics</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"
	l += navlinks
	l += "<a href='/transports-map'>transports map</a>\n\n"
	l += "<a href='/log-collection/tplogs'>transport logs</a>\n\n"
	l += fmt.Sprintf("%s\n",
		shh(" _tpstats"))
	l += htmlend
	return l
}

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
	htmlPageTemplateData1 := htmlPageTemplateData
	htmlPageTemplateData1.Content = htmpl.HTML(skycoinlogohtml)
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
	c.Writer.Write((bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1)))
}

var htmlRewardPageTemplate = `
{{.Page.Content}}
`

/*
<div style='float: right;'>{{.Page.RewardCalc}}</div>
{{.Page.DaysCalc}}
{{.Page.RCal}}
<div style='float: right;'>
reward distribution transaction for {{.Page.MostRecentRewardForDate}} distributed on {{.Page.MostRecentTxIDDate}}
<a href='https://explorer.skycoin.com/app/transaction/{{.Page.MostRecentTxID}}'>{{.Page.MostRecentTxID}}</a>
{{.Page.CSVCheck}}
Previous distributions:
{{.Page.CSVCheck}}
<a href='#top'>top of page</a>
</div>
reward distribution CSV for {{.Page.MostRecentRewardForDate}} distributed on {{.Page.MostRecentTxIDDate}}
{{.Page.LocalCSVCheckWithAnchor}}
<a href='/skycoin-rewards/shares'>Reward Shares</a>  Skycoin per share: {{.Page.SkyPerShare}}
Reward system status:
{{.Page.NextSkywireCliLogRun}}
{{.Page.MostRecentTxnInfo}}
<br>
*/

type Transaction struct {
	Status struct {
		Confirmed   bool `json:"confirmed"`
		Unconfirmed bool `json:"unconfirmed"`
		Height      int  `json:"height"`
		BlockSeq    int  `json:"block_seq"`
	} `json:"status"`
	Time int `json:"time"`
	Txn  struct {
		Timestamp int      `json:"timestamp"`
		Length    int      `json:"length"`
		Type      int      `json:"type"`
		Txid      string   `json:"txid"`
		InnerHash string   `json:"inner_hash"`
		Sigs      []string `json:"sigs"`
		Inputs    []string `json:"inputs"`
		Outputs   []struct {
			Uxid  string `json:"uxid"`
			Dst   string `json:"dst"`
			Coins string `json:"coins"`
			Hours int    `json:"hours"`
		} `json:"outputs"`
	} `json:"txn"`
}

func csvcheck(txid string) string {
	// Make HTTP GET request to locally running instance of Skycoin Explorer API
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:8001/api/transaction?txid=%s", strings.TrimSuffix(txid, "\n")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error making HTTP request to local skycoin-explorer instance: %v\nTrying with explorer.skycoin.com", err)
		// Make HTTP GET request to Skycoin Explorer API
		resp, err = http.Get(fmt.Sprintf("https://explorer.skycoin.com/api/transaction?txid=%s", txid))
		if err != nil {
			msg := fmt.Sprint("Error making HTTP request to explorer.skycoin.com: %v\nCannot check transaction ; %v", err)
			fmt.Fprintf(os.Stderr, msg)
			return msg
		}
	}
	defer resp.Body.Close()

	// Decode JSON response into Transaction struct
	var tx Transaction
	err = json.NewDecoder(resp.Body).Decode(&tx)
	if err != nil {
		msg := fmt.Sprint("Error decoding JSON response: %v\n", err)
		fmt.Fprintf(os.Stderr, msg)
		return msg
	}

	var csvOutputBuilder strings.Builder
	w := csv.NewWriter(&csvOutputBuilder)
	for i, output := range tx.Txn.Outputs {
		if i == len(tx.Txn.Outputs)-1 {
			// skip last line of output containing the from address
			continue
		}
		record := []string{output.Dst, output.Coins}
		if err := w.Write(record); err != nil {
			msg := fmt.Sprint("Error writing CSV record: %v\n", err)
			fmt.Fprintf(os.Stderr, msg)
			return msg
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		msg := fmt.Sprint("Error flushing CSV writer: %v\n", err)
		fmt.Fprintf(os.Stderr, msg)
		return msg
	}
	return csvOutputBuilder.String()
}
func rewardshtmlfunc() []byte {

	l := fmt.Sprintf("page updated: %s\n",
		sh("date"))
	l += fmt.Sprintf("<div style='float: right;'>%s</div>", func() string {
		yearlyTotal := 408000.0
		result := fmt.Sprintf("%g annual reward distribution\nReward total per month:\n", yearlyTotal)

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
		result += fmt.Sprintf("%g Skycoin per day\n", skycoinPerDay)

		return result
	}())
	l += fmt.Sprintf("There are %d days in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day(), time.Now().Month())

	l += fmt.Sprintf("Today is %s %d.\n", time.Now().Month(), time.Now().Day())

	l += fmt.Sprintf("There are %d days left in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()-time.Now().Day(), time.Now().Month())

	l += fmt.Sprintf("%d days in the year %d.\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay(), time.Now().Year())

	l += fmt.Sprintf("Today is day %d.\n", time.Now().YearDay())

	l += fmt.Sprintf("There are %d days remaining in %d\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay()-time.Now().YearDay(), time.Now().Year())

	l += fmt.Sprintf("%s",
		shh(" _rainbowcal"))
	mostrecentrewardfordate, _ := script.FindFiles(`rewards/hist`).MatchRegexp(regexp.MustCompile(".?.?.?.?-.?.?-.?.?.txt")).Last(1).Replace("/", " ").Replace(".txt", "").Column(3).String()
	modTime, _ := os.Stat("rewards/transactions0.txt")
	mostrecenttxid, _ := script.File("rewards/transactions0.txt").Last(1).String()
	l += fmt.Sprintf("<div style='float: right;'>\nreward distribution transaction for %s distributed on %s\n<a href='https://explorer.skycoin.com/app/transaction/%s'>%s</a>\n%s\nPrevious distributions:\n%s\n%s</div>",
		mostrecentrewardfordate,
		modTime.ModTime(),
		mostrecenttxid,
		mostrecenttxid,
		csvcheck(mostrecenttxid),
		shh(" _alltxidlinks"),
		htmltoplink)
	l += fmt.Sprintf("\nreward distribution CSV for %s distributed on %s\n%s\n",
		mostrecentrewardfordate,
		shh(" _mostrecenttxiddate"),
		shh(" _localcsvcheckwithanchor"))
	l += fmt.Sprintf("Reward system status:\n%s\n%s\n",
		shh(" _nextskywireclilogrun"),
		shh(" _mostrecenttxninfo"))
	mostrecenttxninfo := "most recent transaction info placeholder"
	l += fmt.Sprintf("Upcoming distribution data: %s\n%s\n", mostrecentrewardfordate, mostrecenttxninfo)
	l += htmltoplink

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
		Content: htmpl.HTML(l),
	}
	tmplData := map[string]interface{}{
		"Page": htmlPageTemplateData1,
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, tmplData)
	if err != nil {
		fmt.Println("error: ", err)
	}

	return bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1)
}

func sh(cmd string) string {
	return shHTML(fmt.Sprintf(`%s "%s"`, shcmd, cmd))
}
func shh(cmd string) string {
	return shHTML(fmt.Sprintf(`%s "source %s ; %s"`, shcmd, scriptfile, cmd))
}

func shHTML(cmd string) string {
	res, err = script.Exec(cmd).String()
	if err != nil {
		res += fmt.Sprintf("<br><p style='color:red'>error during script.Exec:\n<br> %v\n<br></p>command:\n<br>\n%s\n<br>\n%s", err, cmd, res)
	}
	return res
}
func sex(cmd string) string {
	fmt.Printf("executing command: \n %s", cmd)
	res, err = script.Exec(cmd).String()
	if err != nil {
		res = fmt.Sprintf("error during script.Exec:\n %v\nCommand:\n%s\nResult:\n%s\n", err, cmd, res)
	}
	return res
}

var htmlPageTemplateData htmlTemplateData
var tmpl *htmpl.Template

func Server() {
	fmt.Println("generating in-memory html")

	if dmsgDisc == "" {
		dmsgDisc = skyenv.DmsgDiscAddr
	}
	log := logging.MustGetLogger("dmsghttp")

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

	lis, err := dmsgclient.Listen(uint16(dmsgPort))
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

	r1.GET("/", mainPage)

	r1.GET("/index.html", mainPage)

	/* //consumes too much resources when network is hevily transported
		r1.GET("/transports", func(c *gin.Context) {
			c.Writer.Header().Set("Server", "")
			c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
			c.Writer.Header().Set("Transfer-Encoding", "chunked")
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Flush()
			c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Transport statistics</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"))
				c.Writer.Flush()
				c.Writer.Write([]byte(navlinks))
				c.Writer.Flush()
				tpstats, _ := script.Exec("skywire-cli rtree --stats").Bytes()
				c.Writer.Write(ansihtml.ConvertToHTML(tpstats))
				c.Writer.Flush()
				c.Writer.Write([]byte(htmlend))
				c.Writer.Flush()
	//	c.Writer.Write([]byte(transportstatshtml()))
			return
		})

		r1.GET("/transports-map", func(c *gin.Context) {
	  c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Transport Map</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"))
			c.Writer.Flush()
			c.Writer.Write([]byte(navlinks))
			c.Writer.Flush()
			tpTree, _ := script.Exec("skywire-cli rtree").Bytes()
			c.Writer.Write(ansihtml.ConvertToHTML(tpTree))
			c.Writer.Flush()
			c.Writer.Write([]byte(htmlend))
			c.Writer.Flush()
	//		c.Writer.Write([]byte(transportsmaphtml()))
			return
		})
	*/

	r1.GET("/log-collection", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Content-Type", "text/html;charset=utf-8")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><title>Skywire Survey and Transport Log Collection</title></head>"))
		c.Writer.Flush()
		c.Writer.Write([]byte("<body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n#latest-content-anchor {\n  visibility: hidden;\n}\n</style>\n<pre>"))
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks))
		c.Writer.Flush()
		c.Writer.Write([]byte(fmt.Sprintf("%s\n", shh("_nextskywireclilogrun"))))
		c.Writer.Flush()

		// Initial line count
		initialLineCount, _ := script.File("rewards/skywire-cli-log.txt").CountLines()
		// Read and print the initial lines
		initialContent, _ := script.File("rewards/skywire-cli-log.txt").First(initialLineCount).Bytes()
		c.Writer.Write(ansihtml.ConvertToHTML(initialContent))
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
			currentLineCount, _ := script.File("rewards/skywire-cli-log.txt").CountLines()
			// Check if there are new lines
			if currentLineCount > initialLineCount {
				newContent, _ := script.File("rewards/skywire-cli-log.txt").Last(currentLineCount - initialLineCount).Bytes()
				initialLineCount = currentLineCount
				c.Writer.Write(ansihtml.ConvertToHTML(newContent))
				c.Writer.Flush()
			}
			finished, _ := script.File("rewards/skywire-cli-log.txt").Last(1).MatchRegexp(regexp.MustCompile(".*finished.*")).String()
			if finished != "" {
				break
			}
		}

		c.Writer.Write([]byte(htmltoplink))
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend))
		c.Writer.Flush()
	})

	r1.GET("/log-collection/tree", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><meta charset='UTF-8'><title>Index of Skywire Surveys & Transport Logs</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"))
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks))
		c.Writer.Flush()
		surveycount, _ := script.FindFiles("rewards/log_backups/").Match("node-info.json").CountLines()
		c.Writer.Write([]byte(fmt.Sprintf("Total surveys: %v\n", surveycount)))
		c.Writer.Flush()
		st, _ := script.Exec(`bash -c 'go run fr.go st'`).Bytes()
		c.Writer.Write(ansihtml.ConvertToHTML(st))
		c.Writer.Flush()
		c.Writer.Write([]byte(htmltoplink))
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend))
		c.Writer.Flush()
	})

	r1.GET("/log-collection/tree/:pk", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		var checkKey cipher.PubKey
		err := checkKey.Set(c.Param("pk"))
		if err != nil {
			c.Writer.WriteHeader(http.StatusBadRequest)
			return
		}
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Write([]byte("<!doctype html><html lang=en><head><meta charset='UTF-8'><title>Index of Skywire Surveys & Transport Logs</title></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"))
		c.Writer.Flush()
		c.Writer.Write([]byte(navlinks))
		c.Writer.Flush()
		surveycount, _ := script.FindFiles("rewards/log_backups/").Match("node-info.json").CountLines()
		c.Writer.Write([]byte(fmt.Sprintf("Total surveys: %v\n", surveycount)))
		c.Writer.Flush()
		st, _ := script.Exec(`bash -c 'go run fr.go st -p ` + c.Param("pk") + `'`).Bytes()
		c.Writer.Write(ansihtml.ConvertToHTML(st))
		c.Writer.Flush()
		c.Writer.Write([]byte(htmltoplink))
		c.Writer.Flush()
		c.Writer.Write([]byte(htmlend))
		c.Writer.Flush()
		return
	})

	r1.GET("/log-collection/tplogs", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.WriteHeader(http.StatusOK)
		tploghtmlfunc()
		c.Writer.Write([]byte(*tploghtml))
		return
	})

	r1.GET("/skycoin-rewards", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		l := fmt.Sprintf("page updated: %s\n",
			sh("date"))
		l += fmt.Sprintf("<div style='float: right;'>%s</div>", func() string {
			yearlyTotal := 408000.0
			result := fmt.Sprintf("%g annual reward distribution\nReward total per month:\n", yearlyTotal)

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
			result += fmt.Sprintf("%g Skycoin per day\n", skycoinPerDay)

			return result
		}())
		l += fmt.Sprintf("There are %d days in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day(), time.Now().Month())

		l += fmt.Sprintf("Today is %s %d.\n", time.Now().Month(), time.Now().Day())

		l += fmt.Sprintf("There are %d days left in the month of %s.\n", time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()-time.Now().Day(), time.Now().Month())

		l += fmt.Sprintf("%d days in the year %d.\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay(), time.Now().Year())

		l += fmt.Sprintf("Today is day %d.\n", time.Now().YearDay())

		l += fmt.Sprintf("There are %d days remaining in %d\n", time.Date(time.Now().Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay()-time.Now().YearDay(), time.Now().Year())

		l += fmt.Sprintf("%s",
			shh(" _rainbowcal"))
		rewardtxncsvs, _ := script.FindFiles(`rewards/hist`).MatchRegexp(regexp.MustCompile(".?.?.?.?-.?.?-.?.?_rewardtxn0.csv")).Replace("rewards/hist/", "").Replace("_rewardtxn0.csv", "").Slice()
		for i := len(rewardtxncsvs) - 1; i >= 0; i-- {
			l += "<a href='/skycoin-rewards/hist/" + rewardtxncsvs[i] + "'>" + rewardtxncsvs[i] + "</a>\n"
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
			Content: htmpl.HTML(l),
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

		c.Writer.Write(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1))
		c.Writer.Flush()
		return
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
			c.Writer.Write([]byte("len(wlkeys) == 0"))
			return
		}
		// Read the request body
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("io.ReadAll(c.Request.Body) :\n\n" + string(body) + "\n\nError:\n\n" + err.Error()))
			return
		}
		//check that wallet is running
		status, err := script.Exec("skycoin-cli status").String()
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("skycoin-cli status:\n\n" + status + "\n\nskycoin-cli status error:\n\n" + err.Error()))
			return
		}
		//find all transacion csvs
		f, err := script.FindFiles("rewards/hist").MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice()
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte(`script.FindFiles("rewards/hist").MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice():\n\n` + strings.Join(f, "\n") + "\n\nError:\n\n" + err.Error()))
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
					c.Writer.Write([]byte("skycoin-cli decodeRawTransaction:\n\n" + decoded + "\n\nskycoin-cli decodeRawTransaction error:\n\n" + err.Error()))
					return
				}
				//if all is well, broadcast the transaction
				txid, err := script.Exec("skycoin-cli broadcastTransaction " + string(body)).String()
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte("skycoin-cli broadcastTransaction:\n\n" + txid + "\n\nskycoin-cli broadcastTransaction error:\n\n" + err.Error()))
					return
				}
				//record the transaction ID for that day's reward
				_, err = script.Echo(txid).WriteFile(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1))
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte(`script.Echo(txid).WriteFile(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1))\n\n` + txid + "\n\n" + strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1) + "\n\nerror:\n\n" + err.Error()))
					return
				}
				//record the transaction ID for the reward notification system - append the file!
				_, err = script.Echo(txid).AppendFile("rewards/transactions0.txt")
				if err != nil {
					c.Writer.WriteHeader(http.StatusInternalServerError)
					c.Writer.Write([]byte(`script.Echo(txid).AppendFile("rewards/transactions0.txt")\n\n` + txid + "\n\nerror:\n\n" + err.Error()))
					return
				}
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Write([]byte(txid))
				return
			}
		}
		c.Writer.WriteHeader(http.StatusNotFound)
		h, _ := script.FindFiles("rewards/hist").String()
		c.Writer.Write([]byte("No undistributed rewards csv found.\n\n" + h))
		return
	})

	r1.GET("/skycoin-rewards/csv", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String()
		if strings.TrimRight(active, "\n") == "active" {
			c.Writer.Header().Set("Server", "")
			c.Writer.WriteHeader(http.StatusNotFound)
			return
		}
		c.Writer.Header().Set("Server", "")
		//		c.Writer.WriteHeader(http.StatusOK)
		f, _ := script.FindFiles("rewards/hist").MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice()
		for _, f1 := range f {
			g, err := script.File(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String()
			if err != nil || g == "" || g == "\n" || g == "test" || g == "test\n" {
				c.Writer.Header().Set("Content-Type", "text/plain")
				c.Writer.WriteHeader(http.StatusOK)
				c.Writer.Write([]byte("skycoin-" + f1))
				//		c.Redirect(http.StatusFound, "/skycoin-"+f1)
				return
			}

		}
		c.Writer.WriteHeader(http.StatusNotFound)
		return
	})
	//status of reward system hourly run.
	r1.GET("/skycoin-rewards/s", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String()
		c.JSON(http.StatusOK, gin.H{"active": strings.TrimRight(active, "\n")})
	})
	r1.GET("/health", func(c *gin.Context) {
		runTime = time.Since(startTime)
		nextrun, _ := script.Exec(`bash -c "systemctl status skywire-reward.timer --lines=0 | head -n4 | tail -n1 | sed 's/    Trigger: //g'"`).String()
		prevDuration, _ := script.Exec(`bash -c "systemctl status skywire-reward.service --lines=0 | grep -m1 'Duration' | sed 's/   Duration: //g'"`).String()
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String()
		c.JSON(http.StatusOK, gin.H{
			"frontend_start_time":             startTime,
			"frontend_run_time":               runTime.String(),
			"dmsg_address":                    fmt.Sprintf("%s:%d", pk.String(), dmsgPort),
			"reward_system_active":            strings.TrimRight(active, "\n"),
			"reward_system_next_run":          strings.TrimRight(nextrun, "\n"),
			"reward_system_prev_run_duration": strings.TrimRight(prevDuration, "\n"),
			"whitelisted_keys":                wlkeys,
		})
	})

	r1.GET("/skycoin-rewards/csv/plain", func(c *gin.Context) {
		active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String()
		if strings.TrimRight(active, "\n") == "active" {
			c.Writer.Header().Set("Server", "")
			c.Writer.WriteHeader(http.StatusNotFound)
			return
		}
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Content-Type", "text/plain")
		f, _ := script.FindFiles("rewards/hist").MatchRegexp(regexp.MustCompile(".*_rewardtxn0.csv")).Slice()
		for _, f1 := range f {
			g, _ := script.File(strings.Replace(f1, "_rewardtxn0.csv", ".txt", -1)).String()
			if g != "" || g != "\n" {
				c.Redirect(http.StatusFound, "/skycoin-"+f1)
				return
			}

		}
		c.Writer.WriteHeader(http.StatusNotFound)
		return
	})

	r1.GET("/skycoin-rewards/hist/:date", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		_, err := time.Parse("2006-01-02", c.Param("date"))
		if err != nil {
			if strings.Contains(c.Param("date"), "_rewardtxn0.csv") {
				filetoserve, err := script.File("rewards/hist/" + c.Param("date")).Bytes()
				if err == nil {
					c.Writer.Header().Set("Content-Type", "text/plain")
					c.Writer.WriteHeader(http.StatusOK)
					c.Writer.Flush()
					c.Writer.Write(filetoserve)
					c.Writer.Flush()
					return
				} else {
					fmt.Println("non nil script.File error")
					c.Writer.WriteHeader(http.StatusNotFound)
					c.Writer.Flush()
					return
				}
			}
		}
		rewardfiles, _ := script.FindFiles(`rewards/hist`).Match(c.Param("date")).Slice()
		if len(rewardfiles) == 0 {
			fmt.Println("len rewardfiles == 0")
			c.Writer.WriteHeader(http.StatusNotFound)
			c.Writer.Flush()
			return
		}
		l := ""
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
				thispk, _ := script.Echo(line).Column(2).String()
				share, _ := script.Echo(line).Column(3).String()
				sky, _ := script.Echo(line).Column(4).String()
				l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + strings.TrimRight(share, "\n") + strings.Replace(sky, ",\n", "\n", -1)
			}
		}
		l2, err = script.File("rewards/hist/" + c.Param("date") + "_ineligible.csv").Slice()
		if err == nil {
			l += "\n\nIneligible:\n"
			for _, line := range l2 {
				thispk, _ := script.Echo(line).Column(2).String()
				architecture, _ := script.Echo(line).Column(6).String()
				invalid, _ := script.Echo(line).Match(", , , ,").String()
				if invalid != "" {
					_, err = script.IfExists("rewards/log_backups/" + thispk + "/node-info.json").Echo("").String()
					if err != nil {
						l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + " Survey not found\n"
					} else {
						l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + " Invalid survey\n"
					}
				} else {
					l += "<a id='" + strings.TrimRight(thispk, ",\n") + "'>" + strings.TrimRight(thispk, ",\n") + "</a>," + " Ineligible " + strings.Replace(architecture, ",\n", "\n", -1)
				}
			}
		}
		l += "</div>"

		l1, _ = script.File("rewards/hist/" + c.Param("date") + "_stats.txt").String()
		l += c.Param("date") + "_stats.txt\n" + l1 + "\n"

		l2, _ = script.File("rewards/hist/"+c.Param("date")+"_rewardtxn0.csv").Replace(",", " ").Slice()
		l += c.Param("date") + "_transaction0.csv\n\nSKY Address, Amount\n"
		for _, line := range l2 {
			skyaddr, _ := script.Echo(line).Column(1).String()
			skyamt, _ := script.Echo(line).Column(2).String()
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
			Content: htmpl.HTML(l),
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
		c.Writer.Write(result.Bytes())
		c.Writer.Flush()
		return
	})

	authRoute.GET("/skycoinrewards/hist/:date", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		//override the behavior of `public fallback` for this endpoint
		if len(wlkeys) == 0 {
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.Writer.Write([]byte("len(wlkeys) == 0"))
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
					c.Writer.Write(filetoserve)
					c.Writer.Flush()
					return
				} else {
					fmt.Println("non nil script.File error")
					c.Writer.WriteHeader(http.StatusNotFound)
					c.Writer.Flush()
					return
				}
			}
		}
		rewardfiles, _ := script.FindFiles(`rewards/hist`).Match(c.Param("date")).Slice()
		if len(rewardfiles) == 0 {
			fmt.Println("len rewardfiles == 0")
			c.Writer.WriteHeader(http.StatusNotFound)
			c.Writer.Flush()
			return
		}
		l := ""
		l1, _ := script.File("rewards/hist/" + c.Param("date") + "_stats.txt").String()
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

		l2, _ := script.File("rewards/hist/"+c.Param("date")+"_rewardtxn0.csv").Replace(",", " ").Slice()
		l += c.Param("date") + "_transaction0.csv\n\nSKY Address, Amount\n"
		for _, line := range l2 {
			skyaddr, _ := script.Echo(line).Column(1).String()
			skyamt, _ := script.Echo(line).Column(2).String()
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
			Content: htmpl.HTML(l),
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
		c.Writer.Write(result.Bytes())
		c.Writer.Flush()
		return
	})

	authRoute.GET("/node-info/:pk", func(c *gin.Context) {
		c.Writer.Header().Set("Server", "")
		//override the behavior of `public fallback` for this endpoint
		if len(wlkeys) == 0 {
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.Writer.Write([]byte("len(wlkeys) == 0"))
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
		c.Writer.Write(ni)
		c.Writer.Flush()
		return
	})

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
	faviconBuffer, _ := base64.StdEncoding.DecodeString(faviconBase64)

	r1.GET("/favicon.ico", func(c *gin.Context) {
		_, _ = c.Writer.WriteString(string(faviconBuffer))
	})

	// Start the server using the custom Gin handler
	serve := &http.Server{
		Handler:           &GinHandler{Router: r1},
		ReadHeaderTimeout: 3 * time.Second,
	}

	wg := new(sync.WaitGroup)
	// Start serving
	wg.Add(1)
	go func() {
		fmt.Printf("listening on http://127.0.0.1:%d using gin router\n", webPort)
		r1.Run(fmt.Sprintf(":%d", webPort))
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
					log.WithError(err).Error(fmt.Sprintf("Error fetching %V\nError count: %v", ensureOnlineURL, errCount))
				} else {
					errCount = 0
				}
				if errCount >= 3 {
					log.Fatalf("http server %v unreachable after %v tries ; exiting", ensureOnlineURL, errCount)
					os.Exit(1)
				}
			}
		}()
	}

	wg.Wait()
}

type GinHandler struct {
	Router *gin.Engine
}

func (h *GinHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

type consoleColorModeValue int

var consoleColorMode = autoColor

const (
	autoColor consoleColorModeValue = iota
	disableColor
	forceColor
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

const shcmd = `/usr/bin/bash -c`

var (
	err error
	res string
	cmd string
	// vars that contain generated html pages
	mainhtml          *string
	logcollectinghtml *string
	logtreehtml       *string
	tploghtml         *string
	rewardshtml       *string
	shareshtml        *string
	visorshtml        *string
	// html snippets
	htmlstart string = "<!doctype html><html lang=en><head></head><body style='background-color:black;color:white;'>\n<style type='text/css'>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n<pre>"
	n0        string = "<a id='top' class='anchor' aria-hidden='true' href='#top'></a>"
	n1        string = "  <a href='/'>fiber</a>"
	n2        string = "  <a href='/skycoin-rewards'>skycoin rewards</a>"
	n3        string = "  <a href='/log-collection'>log collection</a>"
	n4        string = "  <a href='/log-collection/tree'>survey index</a>"
	n5        string = "  <a href='/log-collection/tplogs'>transport logging</a>"

	n8          string = "  <a href='https://ut.skywire.skycoin.com/uptimes?v=v2'>uptime tracker</a>"
	n9          string = "\n<br>\n"
	navlinks    string = n1 + n2 + n3 + n4 + n5 + n8 + n9
	htmltoplink string = "<a href='#top'>top of page</a>\n"
	htmlend     string = "</pre></body></html>"
	htmlstyle   string = "<style>\npre {\n  font-family:Courier New;\n  font-size:10pt;\n}\n.af_line {\n  color: gray;\n  text-decoration: none;\n}\n.column {\n  float: left;\n  width: 30%;\n  padding: 10px;\n}\n.row:after {\n  content: '';\n  display: table;\n  clear: both;\n}\n</style>\n"
	bodystyle   string = "<body style='background-color:black;color:white;'>\n"
)

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"

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

func scriptExecBool(s string) bool {
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
				return false
			}
			b, err := strconv.ParseBool(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return b
			}
		}
		return false
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		b, err := strconv.ParseBool(z)
		if err == nil {
			return b
		}
	}

	return false
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
				return uint(i)
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
			return uint(i)
		}
	}
	return uint(0)
}

func shS(s string) string {
	z, err := script.Exec(fmt.Sprintf(`bash -c '%s'`, skyenvfile, s)).String()
	if err == nil {
		return z
	}
	return ""
}

func shB(s string) bool {
	z, err := script.Exec(fmt.Sprintf(`bash -c '%s'`, s)).String()
	if err == nil {
		b, err := strconv.ParseBool(z)
		if err == nil {
			return b
		}
	}

	return false
}

func shA(s string) []string {
	y, err := script.Exec(fmt.Sprintf(`bash -c '%s'`, s)).Slice()
	if err == nil {
		return y
	}
	return []string{}
}

func shI(s string) int {
	z, err := script.Exec(fmt.Sprintf(`bash -c '%s'`, s)).String()
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
func shU(s string) uint {
	z, err := script.Exec(fmt.Sprintf(`bash -c '%s'`, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return uint(i)
		}
	}
	return uint(0)
}

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error"))
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
			c.Writer.Write([]byte("401 Unauthorized"))
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

// {{template "header" .}}
const htmlMainPageTemplate = `
{{ $page := .Page }}<!doctype html><html lang='en'>
{{template "head" .}}
<body title='' style='background-color:black;color:white;'>
<pre><a id='top' class='anchor' aria-hidden='true' href='#top'></a>  <a href='/'>fiber</a>  <a href='/skycoin-rewards'>skycoin rewards</a>  <a href='/log-collection'>log collection</a>  <a href='/log-collection/tree'>survey index</a>  <a href='/log-collection/tplogs'>transport logging</a>  <a href='/transports'>transport stats</a>  <a href='/transports-map'>transport map</a>  <a href='https://ut.skywire.skycoin.com/uptimes?v=v2'>uptime tracker</a><br>
<main>
{{template "this" .}}
</main>
</pre>
</body></html>
`

const htmlFrontPageTemplate = `
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬─┐┌─┐┬ ┬┌─┐┬─┐┌┬┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤   ├┬┘├┤ │││├─┤├┬┘ ││└─┐
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  ┴└─└─┘└┴┘┴ ┴┴└──┴┘└─┘<br>
{{.Page.Content}}
`
const htmlTransportStatsTemplate = `
<a href='/transports-map'>transports map</a>
<a href='/log-collection/tplogs'>transport logs</a>\n\n"
{{.Page.Content}}
`
const htmlLogCollectingTemplate = `
<a href='/transports-map'>transports map</a>
<a href='/log-collection/tplogs'>transport logs</a>\n\n"
{{.Page.Content}}
`
const htmlLogTreeTemplate = `
<a href='/transports-map'>transports map</a>
<a href='/log-collection/tplogs'>transport logs</a>\n\n"
{{.Page.Content}}
`

var htmlHeadTemplate = `<head>
<meta charset='UTF-8'>
<meta name='viewport' content='width=device-width, initial-scale=1.0, maximum-scale=4.9,'>
<title title='{{.Page.Title}}'>{{.Page.Title}}</title>
<style type='text/css'>
pre {
	font-family:mononokiregular;
	font-size:10pt;
}
.af_line {
	color: gray;
	text-decoration: none;
}
.column {
	float: left;
	width: 30%;
	padding: 10px;
}
.row:after {
	content: '';
	display: table;
	clear: both;
}
</style>
<style>
@font-face {
    font-family: 'mononokiregular';
    src: url(data:application/font-woff2;charset=utf-8;base64,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) format('woff2'),
         url(data:application/font-woff;charset=utf-8;base64,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) format('woff'),
         url('font/mononoki-regular-webfont.ttf') format('truetype');
    font-weight: normal;
    font-style: normal;
}
body {
	font-family:mononokiregular;
}
</style>
</head>`

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
