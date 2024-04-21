package devices

import (
	"log"
	"github.com/xxxserxxx/lingo/v2"
)

const (
	Temperatures = "Temperatures" // Device domain for temperature sensors
)

// TODO: Redesign; this is not thread safe, and it's easy to write code that triggers concurrent modification panics. Channels?

var Domains []string = []string{Temperatures}
var _shutdownFuncs []func() error
var _devs map[string][]string
var _defaults map[string][]string
var _startup []func(map[string]string) error
var tr lingo.Translations

// RegisterShutdown stores a function to be called by gotop on exit, allowing
// extensions to properly release resources.  Extensions should register a
// shutdown function IFF the extension is using resources that need to be
// released.  The returned error will be logged, but no other action will be
// taken.
func RegisterShutdown(f func() error) {
	_shutdownFuncs = append(_shutdownFuncs, f)
}

func RegisterStartup(f func(vars map[string]string) error) {
	if _startup == nil {
		_startup = make([]func(map[string]string) error, 0, 1)
	}
	_startup = append(_startup, f)
}

// Startup is after configuration has been parsed, and provides extensions with
// any configuration data provided by the user.  An extension's registered
// startup function should process and populate data at least once so that the
// widgets have a full list of sensors, for (e.g.) setting up colors.
func Startup(vars map[string]string) []error {
	rv := make([]error, 0)
	for _, f := range _startup {
		err := f(vars)
		if err != nil {
			rv = append(rv, err)
		}
	}
	return rv
}

// Shutdown will be called by the `main()` function if gotop is exited
// cleanly.  It will call all of the registered shutdown functions of devices,
// logging all errors but otherwise not responding to them.
func Shutdown() {
	for _, f := range _shutdownFuncs {
		err := f()
		if err != nil {
			log.Print(err)
		}
	}
}

func RegisterDeviceList(typ string, all func() []string, def func() []string) {
	if _devs == nil {
		_devs = make(map[string][]string)
	}
	if _defaults == nil {
		_defaults = make(map[string][]string)
	}
	if _, ok := _devs[typ]; !ok {
		_devs[typ] = []string{}
	}
	_devs[typ] = append(_devs[typ], all()...)
	if _, ok := _defaults[typ]; !ok {
		_defaults[typ] = []string{}
	}
	_defaults[typ] = append(_defaults[typ], def()...)
}

// Return a list of devices registered under domain, where `domain` is one of the
// defined constants in `devices`, e.g., devices.Temperatures.  The
// `enabledOnly` flag determines whether all devices are returned (false), or
// only the ones that have been enabled for the domain.
func Devices(domain string, all bool) []string {
	if all {
		return _devs[domain]
	}
	return _defaults[domain]
}

func SetTr(tra lingo.Translations) {
	tr = tra
}
