//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/transport/network"
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
	Type              network.Type
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

func RunIntegrationTestCase(t *testing.T, testCases []IntegrationTestCase) {
	for _, itc := range testCases {
		startIntegrationTestCase(t, itc)
		resetIntegrationTestCase(t, itc)
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
		env.StopApp(t, app)
	}

	time.Sleep(appStartDelay)
}

func startIntegrationTestCase(t *testing.T, itc IntegrationTestCase) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs(itc.ParticipatingVisorsHostNames)

	for _, tp := range itc.TransportsToAdd {
		env = env.TestVisorAddTp(t, tp)
	}

	for _, appArg := range itc.AppArgsToSet {
		env = env.VisorSetAppArg(t, appArg)
	}

	for _, app := range itc.AppsToRun {
		var pk string
		if app.VisorServerName != "" {
			pk = env.visorPKs[app.VisorServerName]
		}
		env = env.StartApp(t, app, pk)
	}

	time.Sleep(appStartDelay)

	t.Run(itc.Name, func(t *testing.T) {
		itc.Case(t, env)
	})
}
