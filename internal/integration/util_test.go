//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"testing"
	"time"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// appStartDelay is a delay that we wait for apps to fully start
	// and initialize before testing
	appStartDelay = 10 * time.Second
)

// IntegrationTestCase is an integration test case.
type IntegrationTestCase struct {
	Name                         string
	ParticipatingVisorsHostNames []string
	AppsToRun                    []AppToRun
	AppArgsToSet                 []AppArg
	TransportsToAdd              []Transport
	Case                         func(t *testing.T, env *TestEnv)
}

// Transport describes transport to add.
type Transport struct {
	FromVisorHostName string
	ToVisorHostName   string
	Type              tptypes.Type
}

// AppToRun describes app to run.
type AppToRun struct {
	VisorHostName   string
	AppName         string
	VisorServerName string
	LauncherMode    string // "internal", "external", or "" for default
}

// AppArg describes app argument to set.
type AppArg struct {
	VisorHostName string
	AppName       string
	ArgName       string
	Val           string
}

func RunIntegrationTestCase(t *testing.T, testCases []IntegrationTestCase) {
	for i, itc := range testCases {
		startIntegrationTestCase(t, itc)
		resetIntegrationTestCase(t, itc)

		// Brief delay between test cases since containers are restarted in reset
		if i < len(testCases)-1 {
			const cleanupDelay = 1 * time.Second
			t.Logf("Waiting %v between test cases...", cleanupDelay)
			time.Sleep(cleanupDelay)
		}
	}
}

func resetIntegrationTestCase(t *testing.T, itc IntegrationTestCase) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs(itc.ParticipatingVisorsHostNames)

	for _, tp := range itc.TransportsToAdd {
		_ = env.VisorRemoveTransport(tp) //nolint:errcheck
	}

	// TODO(Sir Darkrengarius+ersonp): set all other args to their default values to ensure that everything is as needed
	// would be better to have a method to inject new app into config with default config.
	// this way we may also have just a single generic visor config with no apps and
	// inject apps as we need it for tests.
	for _, appArg := range itc.AppArgsToSet {
		if appArg.ArgName == "netifc" || appArg.ArgName == "passcode" {
			appArg.Val = "remove"
		} else {
			if appArg.Val == "true" {
				appArg.Val = "false"
			}
		}
		env = env.VisorSetAppArg(t, appArg)
	}

	// Restart containers instead of waiting for apps to stop gracefully
	// This is much faster and ensures a clean state for the next test
	visorsToRestart := make(map[string]bool)
	for _, app := range itc.AppsToRun {
		visorsToRestart[app.VisorHostName] = true
	}

	for visor := range visorsToRestart {
		t.Logf("Restarting container %s for fast cleanup", visor)
		if err := env.ContainerRestart(visor); err != nil {
			t.Logf("Warning: failed to restart container %s: %v", visor, err)
		} else {
			t.Logf("Container %s restarted", visor)
		}
	}

	// Brief delay to ensure containers are fully restarted
	time.Sleep(2 * time.Second)

	// Wait for DMSG to be ready on all restarted visors
	// This ensures visors can establish DMSG connections before the next test
	for visor := range visorsToRestart {
		t.Logf("Waiting for DMSG to be ready on %s", visor)
		if err := env.WaitForDmsgDiscoveryEntry(visor, 30*time.Second); err != nil {
			t.Logf("Warning: DMSG not ready on %s after 30s: %v", visor, err)
		} else {
			t.Logf("DMSG ready on %s", visor)
		}
	}
}

func startIntegrationTestCase(t *testing.T, itc IntegrationTestCase) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs(itc.ParticipatingVisorsHostNames)

	for _, appArg := range itc.AppArgsToSet {
		env = env.VisorSetAppArg(t, appArg)
	}

	// Start server apps first (apps without VisorServerName)
	for _, app := range itc.AppsToRun {
		if app.VisorServerName == "" {
			t.Logf("Starting server app %s on %s", app.AppName, app.VisorHostName)
			env = env.StartApp(t, app, "")

			// After app shows "running", give it time to complete Accept() and
			// register routing rules. Status changes to "running" when proc starts,
			// but routing registration happens shortly after.
			const acceptDelay = 3 * time.Second
			time.Sleep(acceptDelay)
			t.Logf("Server app %s on %s ready", app.AppName, app.VisorHostName)
		}
	}

	// Add transports AFTER server apps are ready
	// This ensures the server-side routing rules exist before transport is established
	for _, tp := range itc.TransportsToAdd {
		env = env.TestVisorAddTp(t, tp)
	}

	// Start client apps last (apps with VisorServerName)
	for _, app := range itc.AppsToRun {
		if app.VisorServerName != "" {
			pk := env.visorPKs[app.VisorServerName]
			t.Logf("Starting client app %s on %s connecting to %s", app.AppName, app.VisorHostName, app.VisorServerName)
			env = env.StartApp(t, app, pk)
			t.Logf("Client app %s on %s started", app.AppName, app.VisorHostName)
		}
	}

	time.Sleep(appStartDelay)

	t.Run(itc.Name, func(t *testing.T) {
		itc.Case(t, env)
	})
}
