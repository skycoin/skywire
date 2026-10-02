// Package services pkg/services/flags.go c2-vis-appsvc
package services

import (
	"time"

	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// Flags are the command-line flags every deployment service shares. They
// carry the same names, shorthands and meaning in every service, and each
// service binds them with its own defaults and adds only what is specific
// to it. A --config file overlays them key by key (MergeCommon).
type Flags struct {
	Common
	ConfigPath string
	KeyFile    string
}

// FlagDefaults are a service's defaults for the shared flags.
type FlagDefaults struct {
	// Gen is the `skywire cli config gen` flag that writes this service's
	// config, e.g. "tpd".
	Gen          string
	Addr         string
	Tag          string
	EntryTimeout time.Duration
}

// Bind registers the shared flags on fs.
func (f *Flags) Bind(fs *pflag.FlagSet, d FlagDefaults) {
	fs.StringVarP(&f.ConfigPath, "config", "c", "", "path to a JSON config file; keys it sets override these flags\n\r(generate one with: skywire cli config gen --"+d.Gen+")")
	fs.StringVarP(&f.Addr, "addr", "a", d.Addr, "plain-HTTP listen address")
	fs.StringVar(&f.Mode, "mode", "", "listeners: http|dmsg|dual (default dual with a key, else http; env SKYWIRE_SVC_MODE overrides)")
	fs.Uint16Var(&f.DmsgPort, "dmsg-port", dmsg.DefaultDmsgHTTPPort, "dmsghttp listener port")
	fs.Var(&f.SecKey, "sk", "dmsg secret key")
	fs.StringVar(&f.KeyFile, "keyfile", "", "file holding the secret key (generated if missing)")
	fs.StringVar(&f.Redis, "redis", "", "redis URL of the store (default redis://localhost:6379; with --testing and none, the store is in memory)")
	fs.IntVar(&f.RedisPoolSize, "redis-pool-size", DefaultRedisPoolSize, "redis connection pool size")
	fs.DurationVar((*time.Duration)(&f.EntryTimeout), "entry-timeout", d.EntryTimeout, "how long an entry lives without a refresh")
	fs.StringVarP(&f.LogLevel, "loglvl", "l", "info", "log level [trace|debug|info|warn|error|fatal|panic]")
	fs.StringVar(&f.Tag, "tag", d.Tag, "logging tag")
	fs.StringVarP(&f.MetricsAddr, "metrics", "m", "", "address to serve Prometheus metrics on")
	fs.StringVarP(&f.PprofMode, "pprofmode", "q", "", "[ cpu | mem | mutex | block | trace | http ]")
	fs.StringVarP(&f.PprofAddr, "pprofaddr", "r", "", "address http profiling serves on; alone it implies --pprofmode http (default "+DefaultPprofAddr+")")
	fs.BoolVarP(&f.Testing, "testing", "t", false, "run for a test network: keep entries in memory unless --redis is set")
	fs.BoolVar(&f.TestEnvironment, "test-environment", false, "use the test deployment's defaults instead of production's")
	fs.SetNormalizeFunc(cmdutil.LegacySvcFlagNormalizer)
}

// Resolve returns the Common the flags describe, loading --keyfile
// (generating it if missing) into its secret key.
func (f *Flags) Resolve() (Common, error) {
	c := f.Common
	if f.KeyFile != "" {
		if err := cmdutil.LoadOrGenerateKey(f.KeyFile, &c.SecKey); err != nil {
			return Common{}, err
		}
	}
	return c, nil
}
