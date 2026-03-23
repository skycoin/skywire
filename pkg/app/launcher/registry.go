// Package launcher pkg/app/launcher/registry.go
package launcher

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// AppFunc is an alias for the app function type defined in appcommon.
type AppFunc = appcommon.AppFunc

var appRegistry = make(map[string]AppFunc)

// RegisterApp registers an app function by name.
func RegisterApp(name string, fn AppFunc) {
	if _, exists := appRegistry[name]; exists {
		panic(fmt.Sprintf("app %q already registered", name))
	}
	appRegistry[name] = fn
}

// GetApp returns the app function for the given name, or nil if not found.
func GetApp(name string) (AppFunc, bool) {
	fn, ok := appRegistry[name]
	return fn, ok
}
