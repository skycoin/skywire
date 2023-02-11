// Package visor implements skywire visor.
package visor

import (
	"fmt"
	"net/http"
	"os"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"time"

	"github.com/spf13/cobra"
	"github.com/pkg/profile"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)


func initPProf(log *logging.MasterLogger, profMode string, profAddr string) (stop func()) {
	var optFunc func(*profile.Profile)

	switch profMode {
	case "none", "":
	case "http":
		go func() {
			srv := &http.Server{ //nolint gosec
				Addr:         profAddr,
				Handler:      nil,
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 10 * time.Second,
			}
			err := srv.ListenAndServe()
			log.WithError(err).
				WithField("mode", profMode).
				WithField("addr", profAddr).
				Info("Stopped serving pprof on http.")
		}()
	case "cpu":
		optFunc = profile.CPUProfile
	case "mem":
		optFunc = profile.MemProfile
	case "mutex":
		optFunc = profile.MutexProfile
	case "block":
		optFunc = profile.BlockProfile
	case "trace":
		optFunc = profile.TraceProfile
	}

	if optFunc != nil {
		stop = profile.Start(profile.ProfilePath("./logs/"+logTag), optFunc).Stop
	}

	if stop == nil {
		stop = func() {}
	}
	return stop
}


func logBuildInfo(mLog *logging.MasterLogger) {
	log := mLog.PackageLogger("buildinfo")
	visorBuildInfo = buildinfo.Get()
	if visorBuildInfo.Version != "unknown" {
		log.WithField(" version", visorBuildInfo.Version).WithField("built on", visorBuildInfo.Date).WithField("commit", visorBuildInfo.Commit).Info()
	}
}

func genCompletion(cmd *cobra.Command) {
	switch completion {
	case "bash":
		err := cmd.Root().GenBashCompletion(os.Stdout)
		if err != nil {
			panic(err)
		}
	case "zsh":
		err := cmd.Root().GenZshCompletion(os.Stdout)
		if err != nil {
			panic(err)
		}
	case "fish":
		err := cmd.Root().GenFishCompletion(os.Stdout, true)
		if err != nil {
			panic(err)
		}
	case "powershell":
		err := cmd.Root().GenPowerShellCompletion(os.Stdout)
		if err != nil {
			panic(err)
		}
	}
	//error on unrecognized
	if (completion != "bash") && (completion != "zsh") && (completion != "fish") && (completion != "") {
		fmt.Println("Invalid completion specified:", completion)
		os.Exit(1)
	}

}
