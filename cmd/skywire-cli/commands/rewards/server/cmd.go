// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/cmd.go
package clirewardsserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

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
	ensureOnlineURL  string
	healthOnly       bool
	noUI             bool
	buildTimeout     time.Duration
	log              = logging.MustGetLogger("rewards")
)

var skyenvfile = os.Getenv("SKYENV")

func init() {
	ServerCmd.CompletionOptions.DisableDefaultCmd = true
	ServerCmd.Flags().UintVarP(&webPort, "port", "p", scriptExecUint("${WEBPORT:-80}"), "port to serve")
	ServerCmd.Flags().Uint16VarP(&dmsgPort, "dport", "d", scriptExecUint16("${DMSGPORT:-80}"), "dmsg port to serve")
	ServerCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", scriptExecInt("${DMSGSESSIONS:-1}"), "dmsg sessions")
	msg := "add whitelist keys, comma separated to permit POST of reward transaction to be broadcast"
	if scriptExecArray("${REWARDPKS[@]}") != "" {
		msg += "\n\r"
	}
	ServerCmd.Flags().StringVarP(&wl, "wl", "w", scriptExecArray("${REWARDPKS[@]}"), msg)
	wd, err = os.Getwd()
	if err != nil {
		log.Fatal("Error getting current directory:", err)
	}
	ServerCmd.Flags().StringVarP(&wd, "wd", "W", wd, "location of dir containing 'log_collection' & reward 'hist' dirs")
	ServerCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", deployment.Prod.DmsgDiscovery, "dmsg discovery url")
	ServerCmd.Flags().StringVarP(&ensureOnlineURL, "ensure-online", "O", scriptExecString("${ENSUREONLINE}"), "Exit when the specified URL cannot be fetched;\ni.e. https://fiber.skywire.dev")
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint:errcheck,gosec
	}
	if scriptExecString("${DMSGHTTP_SK}") != "" {
		sk.Set(scriptExecString("${DMSGHTTP_SK}")) //nolint:errcheck,gosec
	}
	ServerCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	ServerCmd.Flags().BoolVar(&healthOnly, "health-only", false, "serve only /health endpoint for testing")
	ServerCmd.Flags().BoolVar(&noUI, "no-ui", false, "skip cogentcore UI extraction and compilation, serve plain HTTP")
	ServerCmd.Flags().DurationVar(&buildTimeout, "build-timeout", 5*time.Minute, "timeout for UI build process")
}

// ServerCmd starts the reward system ui server
var ServerCmd = &cobra.Command{
	Use:   "ui",
	Short: "reward system UI server",
	Long: "skycoin reward system user interface server and skywire network metrics:\n https://fiber.skywire.dev\n" + calvin.AsciiFont("fiber") + func() string {
		if _, err := os.Stat(skyenvfile); err == nil {
			return `run the web application

skyenv file detected: ` + skyenvfile
		}
		return `run the web application

.conf file may also be specified with
SKYENV=/path/to/fiber.conf fiber run`
	}(),
	Run: func(_ *cobra.Command, _ []string) {
		if noUI {
			// Skip UI build, serve plain HTTP
			fmt.Println("Skipping cogentcore UI build (--no-ui flag set)")
			err = fmt.Errorf("UI build skipped")
		} else if !healthOnly {
			outputDir, err = extractFiles()
			if err != nil {
				fmt.Println("Error extracting files:", err)
				return
			}
			fmt.Printf("All files successfully extracted to '%s'.\n", outputDir)

			// Run build with timeout
			ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
			defer cancel()

			buildScript := `cd ` + outputDir + ` || exit 0 ; go mod init fiber.skywire.dev/ui ; go get github.com/skycoin/skywire@develop && go mod tidy && go mod vendor && go run cogentcore.org/core@main build web`
			cmd := exec.CommandContext(ctx, "bash", "-c", buildScript) //nolint:gosec
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			fmt.Printf("Building cogentcore UI (timeout: %v)...\n", buildTimeout)
			err = cmd.Run()
			if ctx.Err() == context.DeadlineExceeded {
				fmt.Printf("Error: UI build timed out after %v\n", buildTimeout)
				err = fmt.Errorf("build timed out after %v", buildTimeout)
			} else if err != nil {
				fmt.Println("Error:", err)
			}
		}

		server(err)
	},
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
					return uint16(i)
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
				return uint16(i)
			}
			return 0
		}
	}
	return uint16(0)
}
