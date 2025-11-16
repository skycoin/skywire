//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	skyvisor "github.com/skycoin/skywire/pkg/visor"
)

type TestEnv struct {
	ctx          context.Context
	cli          *client.Client
	serviceNames []string
	visorNames   []string
	intraNet     string

	// run-time information
	containers   map[string]container.Summary
	visorPKs     map[string]string
	testRunnerID string
	logger       *logging.MasterLogger
	rootDir      string
	dockerDir    string
}

func NewEnv() *TestEnv {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}

	env := &TestEnv{
		ctx:      context.Background(),
		cli:      cli,
		intraNet: "docker_intra",
		serviceNames: []string{
			"/setup-node",
			"/dmsg-server",
			"/dmsg-discovery",
			"/route-finder",
			"/transport-discovery",
			"/address-resolver",
			"/service-discovery",
			"/network-monitor",
			"/uptime-tracker",
		},
		visorNames: []string{
			"/" + visorA,
			"/" + visorB,
			"/" + visorC,
		},
		logger: logging.NewMasterLogger(),
	}

	if err = chdirToRoot(env); err != nil {
		env.logger.Error(err)
	}

	return env
}

func (env *TestEnv) GatherContainersInfo() *TestEnv {
	containers, err := env.cli.ContainerList(env.ctx, container.ListOptions{})
	if err != nil {
		panic(err)
	}

	env.containers = make(map[string]container.Summary)

	for _, container := range containers {
		name := strings.TrimPrefix(container.Names[0], "/")
		env.containers[name] = container

		if name == visorB {
			env.testRunnerID = container.ID
		}
	}

	return env
}

func (env *TestEnv) VisorAppLs(visor string) ([]AppState, error) {

	cliOutput := struct {
		Output []AppState `json:"output,omitempty"`
		Err    *string    `json:"error,omitempty"`
	}{}

	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 app ls --json", visor)
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

// VerifyAppRunning checks if an app is running and fails the test if not
func (env *TestEnv) VerifyAppRunning(t *testing.T, visor, appName string) {
	apps, err := env.VisorAppLs(visor)
	require.NoError(t, err, "Failed to list apps on %s", visor)

	for _, app := range apps {
		if app.App == appName {
			require.Equal(t, "running", app.Status, "App %s on %s is not running (status: %s)", appName, visor, app.Status)
			return
		}
	}

	t.Fatalf("App %s not found on %s", appName, visor)
}

func (env *TestEnv) StartApp(t *testing.T, app AppToRun, pk string) *TestEnv {
	var out string
	var err error

	env.logger.WithField("app", app.AppName).
		WithField("visor", app.VisorHostName).
		WithField("is_vpn_client", app.AppName == skyenv.VPNClientName).
		Info("StartApp called")

	if app.AppName == skyenv.VPNClientName {
		env.logger.Info("Using VPNStart path")
		out, err = env.VPNStart(app, pk)
	} else {
		env.logger.Info("Using VisorAppStart path")
		out, err = env.VisorAppStart(app)
	}

	env.logger.WithField("app", app.AppName).
		WithField("out", out).
		WithField("err", err).
		Info("StartApp completed")

	// If app was already started, we still need to verify it's actually running
	// because the visor might report it as started but the process could be dead
	if err != nil && err.Error() == "app already started" {
		env.logger.WithField("app", app.AppName).
			Warn("App reported as already started, verifying it's actually running...")

		// Try to stop it first
		if app.AppName == skyenv.VPNClientName {
			_, _ = env.VPNStop(app) //nolint:errcheck
		} else {
			_, _ = env.VisorAppStop(app) //nolint:errcheck
		}

		// Wait a moment for it to stop
		time.Sleep(2 * time.Second)

		// Now start it fresh
		if app.AppName == skyenv.VPNClientName {
			out, err = env.VPNStart(app, pk)
		} else {
			out, err = env.VisorAppStart(app)
		}

		env.logger.WithField("app", app.AppName).
			WithField("out_after_restart", out).
			WithField("err_after_restart", err).
			Info("App restarted after 'already started' error")
	}

	if err != nil && err.Error() != "app already started" {
		require.NoError(t, err)
		require.Equal(t, "OK", out)
	}
	return env
}

func (env *TestEnv) StopApp(t *testing.T, app AppToRun) *TestEnv {
	var out string
	var err error

	if app.AppName == skyenv.VPNClientName {
		out, err = env.VPNStop(app)
	} else {
		out, err = env.VisorAppStop(app)
	}
	if err != nil && err.Error() != "app not running" {
		require.NoError(t, err)
		require.Equal(t, "OK", out)
	}
	return env
}

// StopAppBestEffort attempts to stop an app but doesn't fail if it can't.
// Used for test cleanup where we want to attempt cleanup but not fail the test.
func (env *TestEnv) StopAppBestEffort(app AppToRun) *TestEnv {
	if app.AppName == skyenv.VPNClientName {
		_, _ = env.VPNStop(app) //nolint:errcheck
	} else {
		_, _ = env.VisorAppStop(app) //nolint:errcheck
	}
	return env
}

func (env *TestEnv) VisorAppStart(app AppToRun) (string, error) {

	cliOutput := struct {
		Output string  `json:"output,omitempty"`
		Err    *string `json:"error,omitempty"`
	}{}

	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 app start %s --json", app.VisorHostName, app.AppName)
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return "", err
	}
	if cliOutput.Err != nil {
		return "", errors.New(*cliOutput.Err)
	}

	err = env.waitForVisorApp(app)
	if err != nil {
		return "", err
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VisorAppStop(app AppToRun) (string, error) {
	cliOutput := struct {
		Output string  `json:"output,omitempty"`
		Err    *string `json:"error,omitempty"`
	}{}

	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 app stop %s --json", app.VisorHostName, app.AppName)
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return "", err
	}
	if cliOutput.Err != nil {
		return "", errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VisorSetAppArg(t *testing.T, arg AppArg) *TestEnv {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 app arg %s %s %s --json", arg.VisorHostName, arg.ArgName,
		arg.AppName, arg.Val)
	out, err := env.ExecJSONReturnString(cmd)
	require.NoError(t, err)
	require.Equal(t, "OK", out)
	return env
}

func (env *TestEnv) VisorExec(visor, command string) (string, error) {
	// since the output of this command can be anything it is not formatted, so it's advisable to not use the `--json` flag for this one
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 exec %v", visor, command)
	out, err := env.Exec(cmd)
	out = strings.TrimSuffix(out, "\n")

	return out, err
}

func (env *TestEnv) VisorPK(visor string) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 pk --json", visor)
	return env.ExecJSONReturnString(cmd)
}

func (env *TestEnv) VisorHVPK(visor string) ([]string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 hvpk --json", visor)
	return env.ExecJSONReturnSlice(cmd)
}

func (env *TestEnv) VisorCHVPK(visor string) ([]string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 chvpk --json", visor)
	return env.ExecJSONReturnSlice(cmd)
}

func (env *TestEnv) VisorRouteLsRules(visor string) ([]RouteRule, error) {
	cliOutput := struct {
		Output []RouteRule `json:"output,omitempty"`
		Err    *string     `json:"error,omitempty"`
	}{}
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route --json", visor)
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	if cliOutput.Output == nil {
		return []RouteRule{}, nil
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VisorRouteRule(visor string, routeID routing.RouteID) (*RouteRule, error) {
	cliOutput := struct {
		Output []RouteRule `json:"output,omitempty"`
		Err    *string     `json:"error,omitempty"`
	}{}
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route rule %v --json", visor, routeID)
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	return &cliOutput.Output[0], nil
}

func (env *TestEnv) VisorRouteAddAppRule(visor, routeID, localPK, localPort, remotePK, remotePort string) (*RouteKey, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route add-rule app %v %v %v %v %v --json", visor, routeID, localPK, localPort, remotePK, remotePort)
	return env.visorRouteAddRule(cmd)
}

func (env *TestEnv) VisorRouteAddFwdRule(visor, routeID, nextRouteID, nextTpID, localPK, localPort, remotePK, remotePort string) (*RouteKey, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route add-rule fwd %v %v %v %v %v %v %v --json", visor, routeID, nextRouteID, nextTpID, localPK, localPort, remotePK, remotePort)
	return env.visorRouteAddRule(cmd)
}

func (env *TestEnv) VisorRouteAddIntFwdRule(visor, routeID, nextRouteID, nextTpID string) (*RouteKey, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route add-rule intfwd %v %v %v --json", visor, routeID, nextRouteID, nextTpID)
	return env.visorRouteAddRule(cmd)
}

func (env *TestEnv) visorRouteAddRule(cmd string) (*RouteKey, error) {

	cliOutput := struct {
		Output *RouteKey `json:"output,omitempty"`
		Err    *string   `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VisorRouteRmRule(visor string, routeID routing.RouteID) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 route rm-rule %v --json", visor, routeID)
	return env.ExecJSONReturnString(cmd)
}

// TODO(ersonp): figure out a way to write test for this
func (env *TestEnv) VisorHalt(visor string) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 halt --json", visor)
	return env.ExecJSONReturnString(cmd)
}

// TODO(ersonp): figure out a way to write test for this
func (env *TestEnv) VisorStart(visor string) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 start --json", visor)
	return env.ExecJSONReturnString(cmd)
}

func (env *TestEnv) VisorTpType(visor string) ([]tptypes.Type, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp type --json", visor)
	cliOutput := struct {
		Output []tptypes.Type `json:"output,omitempty"`
		Err    *string        `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return []tptypes.Type{}, err
	}
	if cliOutput.Err != nil {
		return []tptypes.Type{}, errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VisorTpLs(visor string) ([]*skyvisor.TransportSummary, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp ls --json", visor)
	return env.visorTpExec(cmd)
}

func (env *TestEnv) VisorTpID(visor string, tpID uuid.UUID) (*skyvisor.TransportSummary, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp id %v --json", visor, tpID)
	output, err := env.visorTpExec(cmd)
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func (env *TestEnv) VisorTpAddDefault(visor string, pk string) (*skyvisor.TransportSummary, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp add %v -f --json", visor, pk)
	output, err := env.visorTpExec(cmd)
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func (env *TestEnv) VisorTpAdd(visor, pk string, tpType tptypes.Type) (*skyvisor.TransportSummary, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp add %s --type %s --force --json", visor, pk, tpType)
	output, err := env.visorTpExec(cmd)
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func (env *TestEnv) VisorTpRm(visor string, tpID uuid.UUID) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp rm -i %v --json", visor, tpID)
	return env.ExecJSONReturnString(cmd)
}

func (env *TestEnv) visorTpExec(cmd string) ([]*skyvisor.TransportSummary, error) {
	cliOutput := struct {
		Output []*skyvisor.TransportSummary `json:"output,omitempty"`
		Err    *string                      `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VPNList(visor string) ([]servicedisc.Service, error) {
	cmd := fmt.Sprintf("/release/skywire cli vpn --rpc %v:3435 list --sdurl http://service-discovery:9098 --json", visor)
	cliOutput := struct {
		Output []servicedisc.Service `json:"output,omitempty"`
		Err    *string               `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VPNStart(app AppToRun, serverPk string) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli vpn --rpc %v:3435 start %v --json", app.VisorHostName, serverPk)
	cliOutput := struct {
		Output VPNStart `json:"output,omitempty"`
		Err    *string  `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return "", err
	}

	// First attempt to wait for app
	err = env.waitForVisorApp(app)

	// If we get an error, check if it's a routing table error
	if err != nil && strings.Contains(err.Error(), "errored") {
		env.logger.WithError(err).Warn("VPN client failed on first attempt, checking for routing errors...")

		// Get client logs to check for routing table errors
		clientLogs, logErr := env.ReadLog(app.VisorHostName)
		if logErr == nil && strings.Contains(clientLogs, "routing table: rule not found") {
			env.logger.Warn("Detected 'routing table: rule not found' error - retrying with transport cleanup")

			// Stop the client app
			env.logger.Info("Stopping VPN client for retry...")
			_, _ = env.VPNStop(app) //nolint:errcheck
			time.Sleep(2 * time.Second)

			// Remove transports from both client and server
			env.logger.Info("Removing transports for clean retry...")
			// Transport removal will be handled by finding and removing existing transports
			// The transport should auto-create when we start the client again

			// Retry starting the client
			env.logger.Info("Retrying VPN client start...")
			err2 := env.ExecJSON(cmd, &cliOutput)
			if err2 != nil {
				return "", fmt.Errorf("retry failed: %w (original error: %v)", err2, err)
			}

			err = env.waitForVisorApp(app)
			if err != nil {
				return "", fmt.Errorf("retry wait failed: %w", err)
			}
			env.logger.Info("VPN client started successfully after retry")
		} else {
			return "", err
		}
	}

	if cliOutput.Output.AppError != "" {
		return cliOutput.Output.AppError, nil
	}
	return "OK", nil
}

func (env *TestEnv) VPNStop(app AppToRun) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli vpn --rpc %v:3435 stop --json", app.VisorHostName)
	return env.ExecJSONReturnString(cmd)
}

func (env *TestEnv) VPNStatus(visor string) (*VPNStatus, error) {
	cmd := fmt.Sprintf("/release/skywire cli vpn --rpc %v:3435 status --json", visor)
	cliOutput := struct {
		Output VPNStatus `json:"output,omitempty"`
		Err    *string   `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, err
	}
	if cliOutput.Err != nil {
		return nil, errors.New(*cliOutput.Err)
	}
	return &cliOutput.Output, nil
}

func (env *TestEnv) TestVisorAddTp(t *testing.T, tp Transport) *TestEnv {
	toPK, ok := env.visorPKs[tp.ToVisorHostName]
	require.True(t, ok)

	_, err := env.VisorTpAdd(tp.FromVisorHostName, toPK, tp.Type)
	require.NoError(t, err)

	return env
}

func (env *TestEnv) VisorGetTransportUUID(tp Transport) ([]*skyvisor.TransportSummary, error) {
	if len(env.visorPKs) == 0 {
		env.GatherVisorPKs(env.visorNames)
	}
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp ls --types %s --pks %s --json", tp.FromVisorHostName, tp.Type, env.visorPKs[tp.ToVisorHostName])
	out, err := env.visorTpExec(cmd)
	if err != nil {
		return nil, err
	}
	// parse output
	if len(out) > 0 {
		return out, nil
	}
	return nil, fmt.Errorf("no transport detected")
}

func (env *TestEnv) VisorRemoveTransport(tp Transport) error {
	tpSums, err := env.VisorGetTransportUUID(tp)
	if err != nil {
		return err
	}

	for _, tpSum := range tpSums {
		_, err := env.VisorTpRm(tp.FromVisorHostName, tpSum.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (env *TestEnv) GatherVisorPKs(visors []string) *TestEnv {
	env.visorPKs = map[string]string{}

	for _, visor := range visors {
		pk, err := env.VisorPK(visor)
		if err != nil {
			panic(err)
		}
		env.visorPKs[visor] = pk
	}

	return env
}

func (env *TestEnv) Exec(cmd string) (string, error) {
	if env.testRunnerID == "" {
		return "", errors.New("env.testRunnerID is empty")
	}

	return env.ExecInContainerByID(cmd, env.testRunnerID)
}

func (env *TestEnv) ExecJSON(cmd string, output interface{}) error {
	result, err := env.execResult(cmd)
	if err != nil {
		return err
	}
	// Parse only stdout to avoid mixing with stderr log messages
	err = json.Unmarshal([]byte(result.Stdout()), &output)
	if err != nil {
		env.logger.Errorf("cliOutput: %v", result.Stdout())
		return err
	}
	return nil
}

func (env *TestEnv) ExecJSONReturnString(cmd string) (string, error) {
	cliOutput := struct {
		Output string  `json:"output,omitempty"`
		Err    *string `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return "", err
	}
	if cliOutput.Err != nil {
		return "", errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) ExecJSONReturnSlice(cmd string) ([]string, error) {
	cliOutput := struct {
		Output []string `json:"output,omitempty"`
		Err    *string  `json:"error,omitempty"`
	}{}
	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return []string{}, err
	}
	if cliOutput.Err != nil {
		return []string{}, errors.New(*cliOutput.Err)
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) ExecInContainerByName(cmd string, containerName string) (string, error) {
	container, ok := env.containers[containerName]
	if !ok {
		return "", fmt.Errorf("no such container %s", containerName)
	}

	return env.ExecInContainerByID(cmd, container.ID)
}

func (env *TestEnv) ExecInContainerByID(cmd string, containerID string) (string, error) {
	result, err := Exec(env.ctx, env.cli, containerID, strings.Split(cmd, " "))
	if err != nil {
		return "", err
	}

	return result.Combined(), nil
}

func (env *TestEnv) execResult(cmd string) (ExecResult, error) {
	if env.testRunnerID == "" {
		return ExecResult{}, errors.New("env.testRunnerID is empty")
	}

	return Exec(env.ctx, env.cli, env.testRunnerID, strings.Split(cmd, " "))
}

func (env *TestEnv) waitForVisorApp(app AppToRun) error {
	const maxAttempts = 12 // 12 * 5s = 60s timeout
	const retryDelay = 5 * time.Second

	env.logger.WithField("app", app.AppName).
		WithField("visor", app.VisorHostName).
		Info("Starting to wait for app...")

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ok, err := env.isVisorAppRunning(app)
		if err != nil {
			env.logger.WithError(err).
				WithField("app", app.AppName).
				WithField("visor", app.VisorHostName).
				WithField("attempt", attempt).
				Warn("Error checking app status")

			// If it's an "errored" status error, dump logs and fail immediately
			if err.Error() != "" && (strings.Contains(err.Error(), "errored") || strings.Contains(err.Error(), "status:")) {
				// Dump visor logs to help debug
				logs, logErr := env.ReadLog(app.VisorHostName)
				if logErr != nil {
					env.logger.WithError(logErr).Warn("Failed to read visor logs")
				} else {
					env.logger.Warnf("\n=== Last 200 lines of %s logs ===\n%s\n=== End logs ===\n", app.VisorHostName, getTailLines(logs, 200))
				}

				// For VPN client errors, also dump the server logs
				if app.AppName == skyenv.VPNClientName && app.VisorServerName != "" {
					serverLogs, serverLogErr := env.ReadLog(app.VisorServerName)
					if serverLogErr != nil {
						env.logger.WithError(serverLogErr).Warn("Failed to read server visor logs")
					} else {
						env.logger.Warnf("\n=== Last 200 lines of %s (server) logs ===\n%s\n=== End logs ===\n", app.VisorServerName, getTailLines(serverLogs, 200))
					}
				}
				return err
			}

			// Other errors might be transient, retry
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return err
		}

		if ok {
			env.logger.WithField("app", app.AppName).
				WithField("visor", app.VisorHostName).
				WithField("attempt", attempt).
				Info("App is running!")
			return nil
		}

		// App not running yet, retry
		env.logger.WithField("app", app.AppName).
			WithField("visor", app.VisorHostName).
			WithField("attempt", attempt).
			Debug("App not running yet, retrying...")

		if attempt < maxAttempts {
			time.Sleep(retryDelay)
		}
	}

	return fmt.Errorf("timeout waiting for app %s on %s to start after %v", app.AppName, app.VisorHostName, time.Duration(maxAttempts)*retryDelay)
}

func (env *TestEnv) isVisorAppRunning(app AppToRun) (bool, error) {
	if app.AppName == skyenv.VPNClientName {
		return env.checkVPNClientStatus(app)
	}
	return env.checkAppStatus(app)
}

func (env *TestEnv) checkAppStatus(app AppToRun) (bool, error) {
	appStates, err := env.VisorAppLs(app.VisorHostName)
	if err != nil {
		return false, err
	}

	env.logger.WithField("app", app.AppName).
		WithField("visor", app.VisorHostName).
		WithField("app_count", len(appStates)).
		Debug("Retrieved app list")

	for _, appState := range appStates {
		if appState.App == app.AppName {
			env.logger.WithField("app", app.AppName).
				WithField("status", appState.Status).
				WithField("detailed_status", appState.DetailedStatus).
				WithField("autostart", appState.AutoStart).
				WithField("port", appState.Port).
				Debug("Found app in list")

			if appState.Status == "errored" {
				// Include detailed status for better debugging
				detail := appState.DetailedStatus
				if detail == "" {
					detail = "(no details available)"
				}
				return false, fmt.Errorf("app %s status: %s, details: %s", app.AppName, appState.Status, detail)
			}
			if appState.Status == "running" {
				return true, nil
			}
		}
	}
	return false, nil
}

func (env *TestEnv) checkVPNClientStatus(app AppToRun) (bool, error) {
	appState, err := env.VPNStatus(app.VisorHostName)
	if err != nil {
		return false, err
	}
	if appState.Status == "errored" {
		// VPNStatus doesn't have detailed_status field, but we can provide context
		return false, fmt.Errorf("VPN client status: %s (check visor logs for details)", appState.Status)
	}
	if appState.Status == "running" {
		return true, nil
	}
	return false, nil
}

// waitForAppStopped waits for an app to be fully stopped and removed from the app list.
// This is critical for test cleanup to prevent race conditions where the next test
// starts before the previous test's apps are fully stopped.
func (env *TestEnv) waitForAppStopped(app AppToRun, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++

		// For VPN client, check VPN status
		if app.AppName == skyenv.VPNClientName {
			_, err := env.VPNStatus(app.VisorHostName)
			if err != nil {
				// If we get an error checking VPN status, it's likely stopped
				env.logger.WithField("app", app.AppName).
					WithField("visor", app.VisorHostName).
					WithField("attempt", attempt).
					Debug("VPN app appears stopped (status check failed)")
				return nil
			}
			// VPN still responding, not stopped yet
			env.logger.WithField("app", app.AppName).
				WithField("visor", app.VisorHostName).
				WithField("attempt", attempt).
				Debug("VPN app still running, waiting...")
			time.Sleep(1 * time.Second)
			continue
		}

		// For regular apps, check app list
		appStates, err := env.VisorAppLs(app.VisorHostName)
		if err != nil {
			env.logger.WithError(err).
				WithField("app", app.AppName).
				WithField("visor", app.VisorHostName).
				Warn("Error checking app list, assuming stopped")
			return nil
		}

		// Look for the app in the list
		found := false
		for _, state := range appStates {
			if state.App == app.AppName {
				found = true
				env.logger.WithField("app", app.AppName).
					WithField("visor", app.VisorHostName).
					WithField("status", state.Status).
					WithField("attempt", attempt).
					Debug("App still in list, waiting for removal...")
				break
			}
		}

		if !found {
			// App not in list anymore, it's stopped
			env.logger.WithField("app", app.AppName).
				WithField("visor", app.VisorHostName).
				WithField("attempt", attempt).
				Debug("App stopped and removed from list")
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for app %s on %s to stop after %v", app.AppName, app.VisorHostName, timeout)
}

func (env *TestEnv) AddDefaultTransports(routerVisor string, skychatNodes []string) *TestEnv {
	for _, node := range skychatNodes {
		_, err := env.VisorTpAddDefault(routerVisor, env.visorPKs[node])
		if err != nil {
			panic(err)
		}
	}

	return env
}

func (env *TestEnv) AddTransports(routerVisor string, visors []string, tpType tptypes.Type) *TestEnv {
	for _, v := range visors {
		if _, err := env.VisorTpAdd(v, env.visorPKs[routerVisor], tpType); err != nil {
			panic(err)
		}
	}

	return env
}

// RemoveAllTransports removes all transports from the specified visors.
// This should be called before restarting visors to clean up stale TPD entries.
func (env *TestEnv) RemoveAllTransports(visors ...string) error {
	for _, visor := range visors {
		tps, err := env.VisorTpLs(visor)
		if err != nil {
			return fmt.Errorf("failed to list transports for %s: %w", visor, err)
		}

		for _, tp := range tps {
			if _, err := env.VisorTpRm(visor, tp.ID); err != nil {
				return fmt.Errorf("failed to remove transport %s from %s: %w", tp.ID, visor, err)
			}
		}
	}

	return nil
}

func (env *TestEnv) ContainerRestart(serviceName ...string) error {
	for _, svcName := range serviceName {
		svc, ok := env.containers[svcName]
		if !ok {
			return errors.New("test-env: service not found")
		}

		timeout := int((2 * time.Minute).Seconds())
		if err := env.cli.ContainerRestart(env.ctx, svc.ID, container.StopOptions{Timeout: &timeout}); err != nil {
			return err
		}
	}

	return nil
}

func (env *TestEnv) SendSkyMessage(senderNode, recipientNode, message string) (resp *http.Response, err error) {
	url := fmt.Sprintf("http://%v:8001/message", senderNode)
	msgData := map[string]string{
		"recipient": env.visorPKs[recipientNode],
		"message":   message,
	}

	data, err := json.Marshal(msgData)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Add("content-type", "application/json")
	hc := http.Client{
		Timeout: 30 * time.Second,
	}
	return hc.Do(req)
}

func (env *TestEnv) NewProxyClient(clientNode, user, password string) (*http.Client, error) {
	auth := proxy.Auth{User: user, Password: password}

	pDialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("%s:1080", clientNode), &auth, proxy.Direct)
	if err != nil {
		return nil, err
	}

	proxyContextDialer := proxyDialer{pDialer}

	c := &http.Client{
		Transport: &http.Transport{DialContext: proxyContextDialer.DialContext},
		Timeout:   HTTPTimeout,
	}

	return c, nil
}

type dialResult struct {
	conn net.Conn
	err  error
}

type proxyDialer struct {
	proxy.Dialer
}

func (p proxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	resCh := make(chan dialResult)

	go func() {
		conn, err := p.Dial(network, address)
		resCh <- dialResult{conn, err}
	}()

	select {
	case <-ctx.Done():
		return nil, context.DeadlineExceeded
	case res := <-resCh:
		return res.conn, res.err
	}
}

func chdirToRoot(env *TestEnv) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	env.rootDir, err = gitRoot(cwd)
	if err != nil {
		return err
	}

	err = os.Chdir(env.rootDir)
	if err != nil {
		return err
	}

	env.dockerDir = filepath.Join(env.rootDir, "docker")
	return nil
}

// getTailLines returns the last n lines from a string
func getTailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
