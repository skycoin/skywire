// Package visor implements skywire visor.
package visor

import (
	"fmt"
	"net/http"
	nethttppprof "net/http/pprof" //nolint:gosec // https://golang.org/doc/diagnostics.html#profiling
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func initPProf(log *logging.MasterLogger, profMode string, profAddr string) (stop func()) {
	switch profMode {
	case "http":
		go func() {
			// Use a dedicated mux to avoid conflicts with DefaultServeMux
			// (which other components like skychat may register on).
			mux := http.NewServeMux()
			mux.HandleFunc("/debug/pprof/", nethttppprof.Index)
			mux.HandleFunc("/debug/pprof/cmdline", nethttppprof.Cmdline)
			mux.HandleFunc("/debug/pprof/profile", nethttppprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", nethttppprof.Symbol)
			mux.HandleFunc("/debug/pprof/trace", nethttppprof.Trace)
			srv := &http.Server{ //nolint:gosec
				Addr:              profAddr,
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
				WriteTimeout:      30 * time.Second,
			}
			log.WithField("addr", profAddr).Info("Serving pprof on http")
			err := srv.ListenAndServe()
			log.WithError(err).
				WithField("mode", profMode).
				WithField("addr", profAddr).
				Info("Stopped serving pprof on http.")
		}()
	}

	stop = func() {}
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
