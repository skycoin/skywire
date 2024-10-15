// Package dmsgcurl pkg/dmsgcurl/flags.go
package dmsgcurl

import (
	"encoding/json"
	"flag"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
)

// ExecName contains the execution name.
const ExecName = "dmsgcurl"

// Version contains the version string.
var Version = buildinfo.Version()

// FlagGroup represents a group of flags.
type FlagGroup interface {
	Name() string
	Init(fs *flag.FlagSet)
}

type startupFlags struct {
	Help bool
}

func (f *startupFlags) Name() string { return "Startup" }

func (f *startupFlags) Init(fs *flag.FlagSet) {
	fs.BoolVar(&f.Help, "help", false, "print this help")
	fs.BoolVar(&f.Help, "h", false, "")
}

type dmsgFlags struct {
	Disc     string
	Sessions int
}

func (f *dmsgFlags) Name() string { return "Dmsg" }

func (f *dmsgFlags) Init(fs *flag.FlagSet) {
	var envServices skywire.EnvServices
	var services skywire.Services
	if err := json.Unmarshal([]byte(skywire.ServicesJSON), &envServices); err == nil {
		if err := json.Unmarshal(envServices.Prod, &services); err == nil {
			f.Disc = services.DmsgDiscovery
		}
	}
	fs.StringVar(&f.Disc, "dmsg-disc", f.Disc, "dmsg discovery `URL`")
	fs.IntVar(&f.Sessions, "dmsg-sessions", 1, "connect to `NUMBER` of dmsg servers")
}

type downloadFlags struct {
	Output string
	Tries  int
	Wait   int
}

func (f *downloadFlags) Name() string { return "Download" }

func (f *downloadFlags) Init(fs *flag.FlagSet) {
	fs.StringVar(&f.Output, "O", ".", "write documents to `FILE`")
	fs.IntVar(&f.Tries, "t", 1, "set number of retries to `NUMBER` (0 unlimits)")
	fs.IntVar(&f.Wait, "w", 0, "wait `SECONDS` between retrievals")
}

type httpFlags struct {
	UserAgent string
}

func (f *httpFlags) Name() string { return "HTTP" }

func (f *httpFlags) Init(fs *flag.FlagSet) {
	fs.StringVar(&f.UserAgent, "U", ExecName+"/"+Version, "identify as `AGENT`")
}
