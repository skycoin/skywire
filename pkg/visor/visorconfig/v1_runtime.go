// Package visorconfig pkg/visor/visorconfig/v1_runtime.go
//
// Non-WASM half of V1's mutator surface. Every method here takes a
// *launcher.AppLauncher (the running app launcher whose ResetConfig
// gets called after each mutation), which would drag in
// pkg/app/launcher's operational deps (process management, bbolt
// app-state store, james-barrow/golang-ipc) — none of which compile
// under GOOS=js.
//
// This file is build-tag-gated to !js so v1.go (the type
// definitions + pure mutators that callers compose into a V1) can
// compile under WASM for in-browser config-generation tooling (the
// apt-repo install page). The visor binary builds native and
// includes both files; the WASM build sees only v1.go.

//go:build !js

package visorconfig

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
)

// UpdateAppAutostart modifies a single app's autostart value within the config and also the given
//
// The updated config gets flushed to file if there are any changes.
// func (v1 *V1) UpdateAppAutostart(appName string, autoStart bool) error {
func (v1 *V1) UpdateAppAutostart(launch *launcher.AppLauncher, appName string, autoStart bool) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	conf := v1.Launcher
	changed := false
	for i := range conf.Apps {
		if conf.Apps[i].Name == appName {
			conf.Apps[i].AutoStart = autoStart
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppArg updates the cli flag of the specified app config and also within the
// The updated config gets flushed to file if there are any changes.
// func (v1 *V1) UpdateAppArg(appName, argName string, value interface{}) error {
func (v1 *V1) UpdateAppArg(launch *launcher.AppLauncher, appName, argName string, value interface{}) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	var configChanged bool
	switch val := value.(type) {
	case string:
		configChanged = updateStringArg(conf, appName, argName, val)
	case bool:
		configChanged = updateBoolArg(conf, appName, argName, val)
	default:
		return fmt.Errorf("invalid arg type %T", value)
	}

	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppArgBatch updates the cli flag of the specified app config and also within the
// The updated config gets flushed to file if there are any changes.
// func (v1 *V1) UpdateAppArg(appName, argName string, value interface{}) error {
func (v1 *V1) UpdateAppArgBatch(launch *launcher.AppLauncher, appName string, args map[string]any) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	var configChanged bool

	for arg, value := range args {
		switch val := value.(type) {
		case string:
			configChanged = updateStringArg(conf, appName, arg, val)
		case bool:
			configChanged = updateBoolArg(conf, appName, arg, val)
		default:
			return fmt.Errorf("invalid arg type %T", value)
		}
	}

	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppEnv sets, replaces, or deletes a KEY=value entry on the
// named app's Env list. An empty value deletes the entry. The
// updated config gets flushed to file if anything changed and the
// launcher is reset so the new env takes effect on the next start.
func (v1 *V1) UpdateAppEnv(launch *launcher.AppLauncher, appName, key, value string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	if key == "" {
		return fmt.Errorf("env key must not be empty")
	}

	conf := v1.Launcher
	configChanged := updateEnvEntry(conf, appName, key, value)
	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppEnvBatch is the multi-key counterpart to UpdateAppEnv.
// One config flush + one launcher reset for the whole batch. An
// empty map value deletes that key.
func (v1 *V1) UpdateAppEnvBatch(launch *launcher.AppLauncher, appName string, env map[string]string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	configChanged := false
	for key, value := range env {
		if key == "" {
			return fmt.Errorf("env key must not be empty")
		}
		if updateEnvEntry(conf, appName, key, value) {
			configChanged = true
		}
	}
	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// updateEnvEntry mutates conf.Apps[i].Env in place: replaces the
// entry whose KEY= prefix matches `key`, deletes it if value is
// empty, or appends a new KEY=value entry. Returns true iff the
// app's Env actually changed (so the caller knows whether to flush).
func updateEnvEntry(conf *Launcher, appName, key, value string) bool {
	prefix := key + "="
	desired := key + "=" + value
	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}
		// Look for an existing entry with this key.
		for j := range conf.Apps[i].Env {
			if !strings.HasPrefix(conf.Apps[i].Env[j], prefix) {
				continue
			}
			if value == "" {
				conf.Apps[i].Env = append(conf.Apps[i].Env[:j], conf.Apps[i].Env[j+1:]...)
				return true
			}
			if conf.Apps[i].Env[j] == desired {
				return false
			}
			conf.Apps[i].Env[j] = desired
			return true
		}
		// No existing entry; nothing to delete.
		if value == "" {
			return false
		}
		conf.Apps[i].Env = append(conf.Apps[i].Env, desired)
		return true
	}
	// App not found — silently no-op, mirrors UpdateAppArg's behavior
	// (it iterates and just doesn't match).
	return false
}

// UpdateAppPort update app port for communicat with visor
func (v1 *V1) UpdateAppPort(launch *launcher.AppLauncher, appName string, port uint16) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	requestPort := routing.Port(port)
	conf := v1.Launcher
	busyPorts := map[routing.Port]bool{}
	appExist := false
	for ind, app := range conf.Apps {
		busyPorts[app.Port] = true
		if app.Name == appName {
			appExist = true
			if requestPort == app.Port {
				break
			}
			if _, ok := busyPorts[requestPort]; ok && requestPort != app.Port {
				return fmt.Errorf("requested port is busy")
			}
			conf.Apps[ind].Port = requestPort
		}
	}
	if !appExist {
		return fmt.Errorf("app not available")
	}

	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// DeleteAppArg Delete entire of args of a custom app
func (v1 *V1) DeleteAppArg(launch *launcher.AppLauncher, appName string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	configChanged := deleteAppArg(conf, appName)

	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// AddAppConfig add new config to apps if name was not same
func (v1 *V1) AddAppConfig(launch *launcher.AppLauncher, appName, binaryName string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	busyPorts := map[routing.Port]bool{}
	for _, app := range conf.Apps {
		busyPorts[app.Port] = true
		if app.Name == appName {
			return fmt.Errorf("the app exist")
		}
	}
	var randomNumber int
	for {
		minn := 10
		maxx := 99
		randomNumber = rand.Intn(maxx-minn+1) + minn             //nolint: gosec
		if _, ok := busyPorts[routing.Port(randomNumber)]; !ok { //nolint: gosec
			break
		}
	}

	conf.Apps = append(conf.Apps, appserver.AppConfig{Name: appName, Binary: binaryName, Port: routing.Port(randomNumber)}) //nolint: gosec

	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppArgsFull replaces the entire Args slice on the named app.
// Used by the universal app-settings panel where the operator edits
// the args as a shell-string and we parse + replace; per-flag
// UpdateAppArg is for targeted CLI surface, not whole-form save.
func (v1 *V1) UpdateAppArgsFull(launch *launcher.AppLauncher, appName string, args []string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}
		conf.Apps[i].Args = args
		launch.ResetConfig(launcher.AppLauncherConfig{
			VisorPK:       v1.PK,
			Apps:          conf.Apps,
			ServerAddr:    conf.ServerAddr,
			DisplayNodeIP: conf.DisplayNodeIP,
		})
		return v1.flush(v1)
	}
	return fmt.Errorf("app %q not found in launcher config", appName)
}

// UpdateAppEnvFull replaces the entire Env slice on the named app.
// Counterpart to UpdateAppArgsFull for the env list. Per-key
// UpdateAppEnv is still available for targeted mutation; this
// method is for whole-form save where deletion of keys not in the
// new list is the intended semantics.
func (v1 *V1) UpdateAppEnvFull(launch *launcher.AppLauncher, appName string, env []string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}
		conf.Apps[i].Env = env
		launch.ResetConfig(launcher.AppLauncherConfig{
			VisorPK:       v1.PK,
			Apps:          conf.Apps,
			ServerAddr:    conf.ServerAddr,
			DisplayNodeIP: conf.DisplayNodeIP,
		})
		return v1.flush(v1)
	}
	return fmt.Errorf("app %q not found in launcher config", appName)
}

// UpdateAppLauncherMode persists the launcher-mode preference for
// an app entry. Empty string clears the override (visor default).
// Validated values: "", "internal", "external".
func (v1 *V1) UpdateAppLauncherMode(launch *launcher.AppLauncher, appName, mode string) error {
	switch mode {
	case "", "internal", "external":
	default:
		return fmt.Errorf("invalid launcher mode %q (must be empty, 'internal', or 'external')", mode)
	}
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}
		conf.Apps[i].LauncherMode = mode
		launch.ResetConfig(launcher.AppLauncherConfig{
			VisorPK:       v1.PK,
			Apps:          conf.Apps,
			ServerAddr:    conf.ServerAddr,
			DisplayNodeIP: conf.DisplayNodeIP,
		})
		return v1.flush(v1)
	}
	return fmt.Errorf("app %q not found in launcher config", appName)
}

// DeleteAppConfig removes an app entry from the launcher config and
// flushes the change. The caller is responsible for stopping the
// running proc (if any) before calling this — V1 doesn't own the
// procmanager. Returns an error if the named app isn't in the
// config so accidental no-ops surface to the operator.
func (v1 *V1) DeleteAppConfig(launch *launcher.AppLauncher, appName string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	idx := -1
	for i, app := range conf.Apps {
		if app.Name == appName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("app %q not found in launcher config", appName)
	}
	conf.Apps = append(conf.Apps[:idx], conf.Apps[idx+1:]...)

	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// updateStringArg updates the cli non-boolean flag of the specified app config and also within the
// It removes argName from app args if value is an empty string.
// The updated config gets flushed to file if there are any changes.
func updateStringArg(conf *Launcher, appName, argName, value string) bool {
	configChanged := false

	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}

		configChanged = true

		argChanged := false
		l := len(conf.Apps[i].Args)
		for j := 0; j < l; j++ {
			equalArgName := conf.Apps[i].Args[j] == argName && j+1 < len(conf.Apps[i].Args)
			if !equalArgName {
				continue
			}

			if value == "" {
				conf.Apps[i].Args = append(conf.Apps[i].Args[:j], conf.Apps[i].Args[j+2:]...)
				j-- //nolint:ineffassign
			} else {
				conf.Apps[i].Args[j+1] = value
			}

			argChanged = true
			break
		}

		if !argChanged && value != "" {
			conf.Apps[i].Args = append(conf.Apps[i].Args, argName, value)
		}

		break
	}

	return configChanged
}

// updateBoolArg updates the cli boolean flag of the specified app config and also within the
// All flag names and values are formatted as "-name=value" to allow arbitrary values with respect to different
// possible default values.
// The updated config gets flushed to file if there are any changes.
func updateBoolArg(conf *Launcher, appName, argName string, value bool) bool {
	const argFmt = "%s=%v"

	configChanged := false

	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}

		// we format it to have a single dash, just to unify representation
		fmtedArgName := argName
		if argName[1] == '-' {
			fmtedArgName = fmtedArgName[1:]
		}

		arg := fmt.Sprintf(argFmt, fmtedArgName, value)

		configChanged = true

		argChanged := false
		for j := 0; j < len(conf.Apps[i].Args); j++ {
			// there shouldn't be such values if config is modified automatically,
			// but might happen if done manually, so we avoid further panic with this check
			if len(conf.Apps[i].Args[j]) < 2 {
				continue
			}

			equalArgName := conf.Apps[i].Args[j][1] != '-' && strings.HasPrefix(conf.Apps[i].Args[j], fmtedArgName)
			if conf.Apps[i].Args[j][1] == '-' {
				equalArgName = strings.HasPrefix(conf.Apps[i].Args[j], "-"+fmtedArgName)
			}

			if !equalArgName {
				continue
			}

			// check next value. currently we store value along with the flag name in a single string,
			// but there're may be some broken configs because of the previous functionality, so we
			// make our best effort to fix this on the go
			if (j + 1) < len(conf.Apps[i].Args) {
				// bool value shouldn't be present there, so we remove it, if it is
				if conf.Apps[i].Args[j+1] == "true" || conf.Apps[i].Args[j+1] == "false" {
					if (j + 2) < len(conf.Apps[i].Args) {
						conf.Apps[i].Args = append(conf.Apps[i].Args[:j+1], conf.Apps[i].Args[j+2:]...)
					} else {
						conf.Apps[i].Args = conf.Apps[i].Args[:j+1]
					}
				}
			}

			conf.Apps[i].Args[j] = arg
			argChanged = true

			break
		}

		if !argChanged {
			conf.Apps[i].Args = append(conf.Apps[i].Args, arg)
		}

		break
	}

	return configChanged
}

// deleteAppArg delete all args of an app by its name
func deleteAppArg(conf *Launcher, appName string) bool {
	var configChanged bool
	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}

		conf.Apps[i].Args = []string{}
		configChanged = true
		break
	}
	return configChanged
}
