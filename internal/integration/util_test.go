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
}

// AppArg describes app argument to set.
type AppArg struct {
	VisorHostName string
	AppName       string
	ArgName       string
	Val           string
}

// hasServerApps returns true if there are any server apps (apps without VisorServerName) in the list.
func hasServerApps(apps []AppToRun) bool {
	for _, app := range apps {
		if app.VisorServerName == "" {
			return true
		}
	}
	return false
}


func RunIntegrationTestCase(t *testing.T, testCases []IntegrationTestCase) {
	for i, itc := range testCases {
		startIntegrationTestCase(t, itc)
		resetIntegrationTestCase(t, itc)
		
		// Add delay between test cases to ensure complete cleanup
		// before the next test starts. This prevents race conditions where
		// the next test's apps start while the previous test's apps are still
		// shutting down (especially important for VPN server which can receive
		// client hello messages during shutdown).
		if i < len(testCases)-1 {
			const cleanupDelay = 5 * time.Second
			t.Logf("Waiting %v between test cases for complete cleanup...", cleanupDelay)
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

	for _, app := range itc.AppsToRun {
		// Stop app and wait for it to actually stop to prevent race conditions
		t.Logf("Stopping app %s on %s", app.AppName, app.VisorHostName)
		env.StopAppBestEffort(app)
		
		// Wait for app to be fully stopped before proceeding
		// This prevents the next test from starting while apps are still shutting down
		t.Logf("Waiting for app %s on %s to fully stop...", app.AppName, app.VisorHostName)
		if err := env.waitForAppStopped(app, 30*time.Second); err != nil {
			// Log but don't fail - this is cleanup, we want to continue even if it times out
			t.Logf("Warning: timeout waiting for app %s to stop: %v", app.AppName, err)
		} else {
			t.Logf("App %s on %s confirmed stopped", app.AppName, app.VisorHostName)
		}
	}

	// Small additional delay to ensure cleanup is complete
	time.Sleep(2 * time.Second)
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
			t.Logf("Server app %s on %s started successfully", app.AppName, app.VisorHostName)
		}
	}
	
	// Give server apps time to fully initialize and start accepting connections
	// This is critical for VPN server which needs to be listening before client connects
	if hasServerApps(itc.AppsToRun) {
		const serverInitDelay = 5 * time.Second
		t.Logf("Waiting %v for server apps to initialize and register with router...", serverInitDelay)
		time.Sleep(serverInitDelay)
	}
	
	// Add transports AFTER server apps are running and have registered with router
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
