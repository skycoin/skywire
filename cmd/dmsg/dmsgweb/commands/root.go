// Package commands cmd/dmsgweb/commands/root.go
package commands

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/bitfield/script"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
)

// Package-level globals shared between the `web` (client) and `srv`
// (server) subcommands. The dmsgweb client runtime now lives in
// pkg/dmsgweb and owns its own state — these remain only because the
// server-side subcommand (dmsgwebsrv.go) still references them.
// Anything only referenced by the client runtime was moved out
// during the extraction step.
var (
	dlog               *logging.Logger
	httpClient         *http.Client
	dmsgC              *dmsg.Client
	closeDmsg          func()
	proxyAddr          string
	filterDomainSuffix string
	sk                 cipher.SecKey
	pk                 cipher.PubKey
	logLvl             string
	webPort            []uint
	proxyPort          uint
	addProxy           string
	resolveDmsgAddr    []string
	isEnvs             bool
	dmsgPort           []uint
	wl                 []string
	wlkeys             []cipher.PubKey
	localPort          []uint
	err                error
	pprofMode          string
	pprofAddr          string
)

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}

func printEnvs(envfile string) {
	if runtime.GOOS == "windows" {
		envfileslice, _ := script.Echo(envfile).Slice() //nolint
		for i := range envfileslice {
			efs, _ := script.Echo(envfileslice[i]).Reject("##").Reject("#-").Reject("# ").Replace("#", "#$").String() //nolint
			if efs != "" && efs != "\n" {
				envfileslice[i] = strings.ReplaceAll(efs, "\n", "")
			}
		}
		envfile = strings.Join(envfileslice, "\n")
	}
	fmt.Println(envfile)
	os.Exit(0)
}
