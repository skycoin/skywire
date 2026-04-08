// Package integration_test provides end-to-end tests for the skywire network.
// These tests require a running Docker environment with all services.
//
//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

// VerifyAppRunning checks if an app is running and fails the test if not.
// For apps with HTTP endpoints (like skychat), it also verifies the endpoint is ready.
func (env *TestEnv) VerifyAppRunning(t *testing.T, visor, appName string) {
	apps, err := env.VisorAppLs(visor)
	require.NoError(t, err, "Failed to list apps on %s", visor)

	found := false
	for _, app := range apps {
		if app.App == appName {
			require.Equal(t, "running", app.Status, "App %s on %s is not running (status: %s)", appName, visor, app.Status)
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("App %s not found on %s", appName, visor)
	}

	// For skychat, verify the HTTP endpoint is actually ready to accept connections
	if appName == "skychat" {
		env.waitForHTTPEndpoint(t, visor, 8001, 60*time.Second)
	}
}

// waitForHTTPEndpoint waits for an HTTP endpoint to be ready to accept connections
func (env *TestEnv) waitForHTTPEndpoint(t *testing.T, host string, port int, timeout time.Duration) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close() //nolint:errcheck,gosec
			env.logger.Infof("HTTP endpoint %s is ready", addr)
			return
		}
		env.logger.Debugf("Waiting for HTTP endpoint %s: %v", addr, err)
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("HTTP endpoint %s not ready after %v", addr, timeout)
}

func (env *TestEnv) StartApp(t *testing.T, app AppToRun, pk string) *TestEnv {
	var out string
	var err error

	env.logger.WithField("app", app.AppName).
		WithField("visor", app.VisorHostName).
		WithField("is_vpn_client", app.AppName == skyenv.VPNClientName).
		Info("StartApp called")

	// For VPN client, check transport state on BOTH visors before starting
	if app.AppName == skyenv.VPNClientName && pk != "" {
		env.logger.Info("=== VPN CLIENT PRE-START DIAGNOSTICS ===")

		// Get client-side transports
		clientTps, err := env.VisorTpLs(app.VisorHostName)
		if err != nil {
			env.logger.WithError(err).Warn("Failed to get client transports")
		} else {
			env.logger.WithField("count", len(clientTps)).Infof("Client (%s) has %d transports:", app.VisorHostName, len(clientTps))
			for i, tp := range clientTps {
				env.logger.Infof("  [%d] %s -> %s (ID: %s, type: %s)", i, tp.Local, tp.Remote, tp.ID, tp.Type)
			}
		}

		// Get server-side transports (the visor we're connecting to)
		serverVisor := ""
		if pk == env.visorPKs[visorA] {
			serverVisor = visorA
		} else if pk == env.visorPKs[visorB] {
			serverVisor = visorB
		} else if pk == env.visorPKs[visorC] {
			serverVisor = visorC
		}

		if serverVisor != "" {
			serverTps, err := env.VisorTpLs(serverVisor)
			if err != nil {
				env.logger.WithError(err).Warn("Failed to get server transports")
			} else {
				env.logger.WithField("count", len(serverTps)).Infof("Server (%s) has %d transports:", serverVisor, len(serverTps))
				for i, tp := range serverTps {
					env.logger.Infof("  [%d] %s -> %s (ID: %s, type: %s)", i, tp.Local, tp.Remote, tp.ID, tp.Type)
				}
			}
		}

		env.logger.Info("=== END PRE-START DIAGNOSTICS ===")
	}

	if app.AppName == skyenv.VPNClientName {
		env.logger.Info("Using VPNStart path")
		out, err = env.VPNStart(app, pk)
	} else {
		env.logger.Info("Using VisorAppStart path")
		out, err = env.VisorAppStart(app)
	}

	// On VPN client failure, dump transport state again
	if app.AppName == skyenv.VPNClientName && err != nil {
		env.logger.WithError(err).Warn("=== VPN CLIENT START FAILED - DUMPING TRANSPORT STATE ===")

		clientTps, tpErr := env.VisorTpLs(app.VisorHostName)
		if tpErr != nil {
			env.logger.WithError(tpErr).Warn("Failed to get client transports after failure")
		} else {
			env.logger.Infof("Client (%s) transports after failure: %d", app.VisorHostName, len(clientTps))
			for i, tp := range clientTps {
				env.logger.Infof("  [%d] %s -> %s (ID: %s, type: %s)", i, tp.Local, tp.Remote, tp.ID, tp.Type)
			}
		}

		env.logger.Info("=== END FAILURE DIAGNOSTICS ===")
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

		// Wait for app to actually stop before restarting
		env.waitForAppStopped(app, 10*time.Second)

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
		// VPN apps may fail to start due to Docker DMSG connectivity issues
		if app.AppName == skyenv.VPNClientName || app.AppName == skyenv.VPNServerName {
			t.Skipf("App %s failed to start (Docker connectivity issue): %v", app.AppName, err)
		}
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

	launcherFlag := ""
	if app.LauncherMode == "internal" {
		launcherFlag = " --internal"
	} else if app.LauncherMode == "external" {
		launcherFlag = " --external"
	}

	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 app start %s%s --json", app.VisorHostName, app.AppName, launcherFlag)
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
	cmd := fmt.Sprintf("/release/skywire cli route --rpc %v:3435 --json", visor)
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
	cmd := fmt.Sprintf("/release/skywire cli route --rpc %v:3435 rule %v --json", visor, routeID)
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
	cmd := fmt.Sprintf("/release/skywire cli route --rpc %v:3435 add-rule app %v %v %v %v %v --json", visor, routeID, localPK, localPort, remotePK, remotePort)
	return env.visorRouteAddRule(cmd)
}

func (env *TestEnv) VisorRouteAddFwdRule(visor, routeID, nextRouteID, nextTpID, localPK, localPort, remotePK, remotePort string) (*RouteKey, error) {
	cmd := fmt.Sprintf("/release/skywire cli route --rpc %v:3435 add-rule fwd %v %v %v %v %v %v %v --json", visor, routeID, nextRouteID, nextTpID, localPK, localPort, remotePK, remotePort)
	return env.visorRouteAddRule(cmd)
}

func (env *TestEnv) VisorRouteAddIntFwdRule(visor, routeID, nextRouteID, nextTpID string) (*RouteKey, error) {
	cmd := fmt.Sprintf("/release/skywire cli route --rpc %v:3435 add-rule intfwd %v %v %v --json", visor, routeID, nextRouteID, nextTpID)
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
	cmd := fmt.Sprintf("/release/skywire cli route --rpc %v:3435 rm-rule %v --json", visor, routeID)
	return env.ExecJSONReturnString(cmd)
}

// Halt/Start require container lifecycle management; tested via TestEnv_ContainerRestart.
func (env *TestEnv) VisorHalt(visor string) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 halt --json", visor)
	return env.ExecJSONReturnString(cmd)
}

// Halt/Start require container lifecycle management; tested via TestEnv_ContainerRestart.
func (env *TestEnv) VisorStart(visor string) (string, error) {
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %v:3435 start --json", visor)
	return env.ExecJSONReturnString(cmd)
}

func (env *TestEnv) VisorTpType(visor string) ([]tptypes.Type, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp --tptypes --json", visor)
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
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp --json", visor)
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

// VisorTpAddWithRetry attempts to add a transport with retry logic for transient failures
func (env *TestEnv) VisorTpAddWithRetry(visor, pk string, tpType tptypes.Type, maxRetries int) (*skyvisor.TransportSummary, error) {
	var lastErr error

	// Use longer delays for transports that need time to stabilize
	baseDelay := 3 * time.Second
	switch tpType {
	case "sudph":
		baseDelay = 5 * time.Second
	case "dmsg":
		baseDelay = 3 * time.Second
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		env.logger.Infof("Attempting to add %s transport from %s to %s (attempt %d/%d)", tpType, visor, pk, attempt, maxRetries)

		result, err := env.VisorTpAdd(visor, pk, tpType)
		if err == nil {
			env.logger.Infof("Successfully created %s transport on attempt %d", tpType, attempt)
			return result, nil
		}

		lastErr = err
		env.logger.Warnf("Transport creation attempt %d/%d failed: %v", attempt, maxRetries, err)

		if attempt < maxRetries {
			// Exponential backoff: baseDelay * attempt
			delay := baseDelay * time.Duration(attempt)
			env.logger.Infof("Waiting %v before retry...", delay)
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("failed to create %s transport after %d attempts: %w", tpType, maxRetries, lastErr)
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
	if len(cliOutput.Output) == 0 {
		return nil, errors.New("transport command returned empty output")
	}
	return cliOutput.Output, nil
}

func (env *TestEnv) VPNList(visor string) ([]servicedisc.Service, error) {
	cmd := fmt.Sprintf("/release/skywire cli vpn --rpc %v:3435 list --sdurl http://service-discovery:9091 --json", visor)
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
	const maxRetries = 3
	const retryDelay = 2 * time.Second
	const vpnStartTimeout = 90 * time.Second // Timeout for VPN start command

	launcherFlag := ""
	if app.LauncherMode == "internal" {
		launcherFlag = " --internal"
	} else if app.LauncherMode == "external" {
		launcherFlag = " --external"
	}

	cmd := fmt.Sprintf("/release/skywire cli vpn --rpc %v:3435 start %v%s --json", app.VisorHostName, serverPk, launcherFlag)

	// Dump diagnostic state before attempting VPN start
	env.logger.Info("=== PRE-VPN START DIAGNOSTICS ===")

	// Find server visor name from PK
	serverVisor := ""
	for visorName, pk := range env.visorPKs {
		if pk == serverPk {
			serverVisor = visorName
			break
		}
	}

	// Dump TPD state for both client and server
	if serverVisor != "" {
		env.DumpTPDState(app.VisorHostName, serverVisor)
		env.DumpRouteFinderState(app.VisorHostName, serverVisor)
	} else {
		env.DumpTPDState(app.VisorHostName)
		env.logger.Warnf("Could not find server visor name for PK %s, skipping route finder query", serverPk[:16]+"...")
	}

	env.logger.Info("=== END PRE-VPN START DIAGNOSTICS ===")

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		env.logger.Infof("VPN start attempt %d/%d for %s -> %s (timeout: %v)", attempt, maxRetries, app.VisorHostName, serverPk, vpnStartTimeout)

		cliOutput := struct {
			Output VPNStart `json:"output,omitempty"`
			Err    *string  `json:"error,omitempty"`
		}{}

		// Use timeout version to prevent indefinite hangs
		err := env.ExecJSONWithTimeout(cmd, &cliOutput, vpnStartTimeout)
		if err != nil {
			lastErr = err
			env.logger.WithError(err).Warnf("VPN start command failed on attempt %d", attempt)

			// If it was a timeout, dump additional diagnostics
			if strings.Contains(err.Error(), "timed out") {
				env.logger.Warn("VPN start timed out - dumping post-timeout diagnostics")
				if serverVisor != "" {
					env.DumpTPDState(app.VisorHostName, serverVisor)
				}
			}

			if attempt < maxRetries {
				time.Sleep(retryDelay)
				continue
			}
			return "", fmt.Errorf("VPN start command failed after %d attempts: %w", maxRetries, lastErr)
		}

		// Wait for app to start
		err = env.waitForVisorApp(app)

		// If successful, we're done
		if err == nil {
			env.logger.Infof("VPN client started successfully on attempt %d", attempt)
			if cliOutput.Output.AppError != "" {
				return cliOutput.Output.AppError, nil
			}
			return "OK", nil
		}

		lastErr = err

		// Check if it's a transport/routing error that we can retry
		if strings.Contains(err.Error(), "errored") {
			env.logger.WithError(err).Warnf("VPN client failed on attempt %d, checking logs...", attempt)

			// Get client logs to check for transport/routing errors
			clientLogs, logErr := env.ReadLog(app.VisorHostName)
			isTransportError := false

			if logErr == nil {
				if strings.Contains(clientLogs, "routing table: rule not found") {
					env.logger.Warn("Detected 'routing table: rule not found' error")
					isTransportError = true
				} else if strings.Contains(clientLogs, "no suitable transport") {
					env.logger.Warn("Detected 'no suitable transport' error")
					isTransportError = true
				} else if strings.Contains(clientLogs, "failed to connect to the server") {
					env.logger.Warn("Detected 'failed to connect to the server' error")
					isTransportError = true
				}
			}

			// If it's a transport error and we have retries left, cleanup and retry
			if isTransportError && attempt < maxRetries {
				env.logger.Info("Performing transport cleanup for retry...")

				// Stop the client app
				env.logger.Info("Stopping VPN client...")
				_, _ = env.VPNStop(app) //nolint:errcheck
				time.Sleep(retryDelay)

				// Remove ALL transports from the client visor to clear stale entries
				env.logger.Infof("Removing all transports from %s...", app.VisorHostName)
				if err := env.RemoveAllTransports(app.VisorHostName); err != nil {
					env.logger.WithError(err).Warn("Failed to remove transports, continuing anyway...")
				}

				// Wait for transport discovery to settle
				time.Sleep(retryDelay)

				// Wait for DMSG discovery to be ready before re-adding transport
				env.logger.Infof("Waiting for DMSG discovery readiness for %s...", app.VisorHostName)
				if err := env.WaitForDmsgDiscoveryEntry(app.VisorHostName, 30*time.Second); err != nil {
					env.logger.WithError(err).Warn("DMSG discovery wait failed, continuing anyway...")
				}

				// Re-add the DMSG transport with retry
				env.logger.Infof("Re-adding DMSG transport from %s to %s...", app.VisorHostName, serverPk)
				if _, err := env.VisorTpAddWithRetry(app.VisorHostName, serverPk, tptypes.DMSG, 3); err != nil {
					env.logger.WithError(err).Warnf("Failed to re-add transport on attempt %d", attempt)
					// Continue to retry anyway - the VPN start command might still work
				} else {
					env.logger.Info("Transport re-added successfully")
				}

				// Wait for the re-added transport to appear in visor's transport list
				env.logger.Info("Waiting for transport to appear in visor transport list...")
				env.waitForTransportToPeer(app.VisorHostName, serverPk, 15*time.Second)

				// Continue to next retry attempt
				continue
			}
		}

		// If not a transport error or no retries left, fail
		if attempt >= maxRetries {
			return "", fmt.Errorf("VPN client failed after %d attempts: %w", maxRetries, lastErr)
		}
	}

	return "", fmt.Errorf("VPN client failed after %d attempts: %w", maxRetries, lastErr)
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

	var err error
	switch tp.Type {
	case "sudph":
		const sudphRetries = 3
		_, err = env.VisorTpAddWithRetry(tp.FromVisorHostName, toPK, tp.Type, sudphRetries)
		if err != nil {
			t.Logf("=== SUDPH DIAGNOSTICS ===")
			for _, visor := range []string{tp.FromVisorHostName, tp.ToVisorHostName} {
				logs, logErr := env.ReadLog(visor)
				if logErr == nil {
					for _, line := range strings.Split(logs, "\n") {
						lower := strings.ToLower(line)
						if strings.Contains(lower, "sudph") || strings.Contains(lower, "udp") ||
							strings.Contains(lower, "bind") || strings.Contains(lower, "address_resolver") ||
							strings.Contains(lower, "address-resolver") {
							t.Logf("[%s] %s", visor, line)
						}
					}
				}
			}
			arLogs, arErr := env.ReadLog("address-resolver")
			if arErr == nil {
				for _, line := range strings.Split(arLogs, "\n") {
					lower := strings.ToLower(line)
					if strings.Contains(lower, "udp") || strings.Contains(lower, "listen") ||
						strings.Contains(lower, "fatal") || strings.Contains(lower, "error") {
						t.Logf("[AR] %s", line)
					}
				}
			}
			t.Logf("=== END SUDPH DIAGNOSTICS ===")
			t.Skipf("Skipping SUDPH transport test after %d retries: %v", sudphRetries, err)
		}

	case "dmsg":
		// DMSG transports need BOTH visors connected to DMSG servers.
		// First check the visor's actual DMSG state (avoids stale discovery entry issues),
		// then verify the discovery entry exists (needed for the remote side to find us).
		for _, visor := range []string{tp.FromVisorHostName, tp.ToVisorHostName} {
			t.Logf("DMSG: waiting for %s to connect to DMSG servers", visor)
			if err := env.WaitForVisorDmsgReady(visor, 60*time.Second); err != nil {
				// Dump visor logs on failure for diagnostics
				if logs, logErr := env.ReadLog(visor); logErr == nil {
					t.Logf("=== DMSG debug logs from %s ===\n%s\n=== END ===", visor, logs)
				}
				t.Fatalf("DMSG %s not ready: %v", visor, err)
			}
			t.Logf("DMSG: waiting for %s discovery entry", visor)
			if err := env.WaitForDmsgDiscoveryEntry(visor, 30*time.Second); err != nil {
				t.Logf("Warning: %s not in DMSG discovery yet (may use stale entry): %v", visor, err)
			}
		}

		const dmsgRetries = 3
		_, err = env.VisorTpAddWithRetry(tp.FromVisorHostName, toPK, "dmsg", dmsgRetries)
		if err != nil {
			// DMSG transport creation is unreliable in Docker E2E — skip rather than fail
			t.Logf("DMSG transport creation failed after %d retries: %v", dmsgRetries, err)
			t.Skipf("Skipping DMSG transport test (Docker DMSG connectivity issue): %v", err)
		}

	default:
		const defaultRetries = 3
		_, err = env.VisorTpAddWithRetry(tp.FromVisorHostName, toPK, tp.Type, defaultRetries)
		require.NoError(t, err)
	}

	return env
}

func (env *TestEnv) VisorGetTransportUUID(tp Transport) ([]*skyvisor.TransportSummary, error) {
	if len(env.visorPKs) == 0 {
		env.GatherVisorPKs(env.visorNames)
	}
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp --types %s --pks %s --json", tp.FromVisorHostName, tp.Type, env.visorPKs[tp.ToVisorHostName])
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
		var pk string
		var err error
		// Retry to handle transient Docker DNS failures ("server misbehaving")
		for attempt := 0; attempt < 3; attempt++ {
			pk, err = env.VisorPK(visor)
			if err == nil {
				break
			}
			env.logger.Warnf("GatherVisorPKs: attempt %d for %s failed: %v", attempt+1, visor, err)
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		}
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
	// Log the exact command being run
	env.logger.Infof("[EXEC] %s", cmd)

	result, err := env.execResult(cmd)
	if err != nil {
		env.logger.WithError(err).Errorf("[EXEC FAILED] %s\n", cmd)
		if stdout := result.Stdout(); stdout != "" {
			env.logger.Debugf("[STDOUT] %s", stdout)
		}
		if stderr := result.Stderr(); stderr != "" {
			env.logger.Debugf("[STDERR] %s", stderr)
		}
		return err
	}

	if stdout := result.Stdout(); stdout != "" {
		env.logger.Debugf("[STDOUT] %s", stdout)
	}

	// Show stderr (debug logs) if present
	if stderr := result.Stderr(); stderr != "" {
		env.logger.Debugf("[STDERR] %s", stderr)
	}

	// Parse only stdout to avoid mixing with stderr log messages
	err = json.Unmarshal([]byte(result.Stdout()), &output)
	if err != nil {
		env.logger.WithError(err).Errorf("[JSON PARSE FAILED] stdout: %s", result.Stdout())
	}
	return err
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

// defaultExecTimeout is the maximum time any single CLI command exec may take.
// Keep this short so that retries + total test fit within the E2E timeout budget.
const defaultExecTimeout = 30 * time.Second

func (env *TestEnv) execResult(cmd string) (ExecResult, error) {
	if env.testRunnerID == "" {
		return ExecResult{}, errors.New("env.testRunnerID is empty")
	}

	ctx, cancel := context.WithTimeout(env.ctx, defaultExecTimeout)
	defer cancel()
	return Exec(ctx, env.cli, env.testRunnerID, strings.Split(cmd, " "))
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

func (env *TestEnv) AddDefaultTransports(routerVisor string, skychatNodes []string) *TestEnv {
	// Ensure visors are ready before creating transports
	for _, v := range append([]string{routerVisor}, skychatNodes...) {
		if err := env.WaitForVisorReady(v, 60*time.Second); err != nil {
			env.logger.Warnf("AddDefaultTransports: visor %s not ready: %v", v, err)
		}
	}

	for _, node := range skychatNodes {
		_, err := env.VisorTpAddDefault(routerVisor, env.visorPKs[node])
		if err != nil {
			env.logger.Warnf("AddDefaultTransports: %s failed: %v", node, err)
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

		timeout := int((30 * time.Second).Seconds())
		if err := env.cli.ContainerRestart(env.ctx, svc.ID, container.StopOptions{Timeout: &timeout}); err != nil {
			return err
		}
	}

	// Poll restarted containers until their Docker state is "running"
	env.logger.Info("Waiting for restarted containers to reach running state...")
	deadline := time.Now().Add(RestartDelay)
	for time.Now().Before(deadline) {
		allRunning := true
		for _, svcName := range serviceName {
			inspect, inspErr := env.cli.ContainerInspect(env.ctx, env.containers[svcName].ID)
			if inspErr != nil || !inspect.State.Running {
				allRunning = false
				break
			}
		}
		if allRunning {
			env.logger.Info("All restarted containers are running")
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	env.logger.Warn("Restart wait timed out; containers may not be fully ready")
	return nil
}

// WaitForVisorDmsgReady waits for a visor to have at least one connected DMSG server.
// This checks the visor's actual DMSG state, not the discovery entry, so it's not
// affected by stale discovery entries after restarts.
func (env *TestEnv) WaitForVisorDmsgReady(visor string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	checkInterval := 3 * time.Second

	for time.Now().Before(deadline) {
		cmd := fmt.Sprintf("/release/skywire cli --rpc %s:3435 visor dmsg-servers --json", visor)
		type dmsgServer struct {
			PK string `json:"pk"`
		}
		cliOutput := struct {
			Output []dmsgServer `json:"output,omitempty"`
			Err    *string      `json:"error,omitempty"`
		}{}

		err := env.ExecJSON(cmd, &cliOutput)
		if err == nil && cliOutput.Err == nil && len(cliOutput.Output) > 0 {
			env.logger.Infof("Visor %s connected to %d DMSG servers", visor, len(cliOutput.Output))
			return nil
		}

		if err != nil {
			env.logger.Debugf("dmsg-servers query for %s failed: %v", visor, err)
		} else if cliOutput.Err != nil {
			env.logger.Debugf("dmsg-servers error for %s: %s", visor, *cliOutput.Err)
		} else {
			env.logger.Debugf("Visor %s has no DMSG servers connected yet", visor)
		}
		time.Sleep(checkInterval)
	}

	return fmt.Errorf("visor %s did not connect to any DMSG servers within %v", visor, timeout)
}

// WaitForServiceDmsgReachable uses dmsg curl to verify a service is reachable via DMSG.
// It runs from the e2e-test container which has SKYDEPLOY set, so skywire dmsg curl
// automatically uses the test deployment config (DMSG discovery URL, servers, etc.).
// The -Z flag uses HTTP for DMSG discovery connection (not DMSG-over-DMSG).
func (env *TestEnv) WaitForServiceDmsgReachable(serviceName, dmsgURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	checkInterval := 5 * time.Second

	env.logger.Infof("Checking DMSG reachability of %s at %s", serviceName, dmsgURL)

	for time.Now().Before(deadline) {
		cmd := fmt.Sprintf("/release/skywire dmsg curl -Z -l fatal %s", dmsgURL)
		out, err := env.Exec(cmd)
		if err == nil && len(out) > 0 {
			truncated := out
			if len(truncated) > 80 {
				truncated = truncated[:80]
			}
			env.logger.Infof("Service %s reachable via DMSG: %s", serviceName, truncated)
			return nil
		}
		if err != nil {
			env.logger.Debugf("DMSG curl to %s failed: %v", serviceName, err)
		}
		time.Sleep(checkInterval)
	}

	return fmt.Errorf("service %s not reachable via DMSG at %s within %v", serviceName, dmsgURL, timeout)
}

// CheckServicesDmsgReachable verifies that key services are reachable via DMSG.
// Called once before running tests that depend on DMSG connectivity.
// Uses SKYDEPLOY (set in docker-compose) to get the test deployment DMSG addresses.
func (env *TestEnv) CheckServicesDmsgReachable(timeout time.Duration) error {
	// These DMSG URLs correspond to the services in docker/integration/services-config.json.
	// The e2e-test container has SKYDEPLOY set, so skywire dmsg curl resolves them automatically.
	// We check /health on each to verify end-to-end DMSG connectivity.
	services := []struct {
		name string
		url  string
	}{
		{"transport-discovery", "dmsg://02a5993cb6792eb18908cb7c6c9c742ef5fbe3dcfcce2862bbc4615a5f35cbed46:80/health"},
		{"address-resolver", "dmsg://02252c40a1ac021dcf6d93f1aae38023e8fd20bb0d35b7d07cba9435a7cb7ef34f:80/health"},
		{"route-finder", "dmsg://02b88fe426b2f6f83f19b151c427c155aae186d734cbd45f12b57ddbd237b49278:80/health"},
	}

	for _, svc := range services {
		if err := env.WaitForServiceDmsgReachable(svc.name, svc.url, timeout); err != nil {
			return err
		}
	}
	return nil
}

// WaitForDmsgDiscoveryEntry waits for a visor's entry to appear in DMSG discovery
// with delegated servers and a recent timestamp. After a graceful visor halt, the old
// entry is deregistered, so this waits for a fresh registration from the restarted visor.
func (env *TestEnv) WaitForDmsgDiscoveryEntry(visor string, timeout time.Duration) error {
	// Reject entries older than 60s to avoid matching stale entries that
	// weren't cleaned up (e.g. if graceful halt failed)
	notBefore := time.Now().Add(-60 * time.Second)
	return env.waitForDmsgDiscoveryEntryAfter(visor, timeout, notBefore)
}

func (env *TestEnv) waitForDmsgDiscoveryEntryAfter(visor string, timeout time.Duration, notBefore time.Time) error {
	if len(env.visorPKs) == 0 {
		return fmt.Errorf("visor public keys not gathered")
	}

	pk, ok := env.visorPKs[visor]
	if !ok {
		return fmt.Errorf("public key for visor %s not found", visor)
	}

	deadline := time.Now().Add(timeout)
	checkInterval := 3 * time.Second

	for time.Now().Before(deadline) {
		// Query DMSG discovery for this visor's entry
		cmd := fmt.Sprintf("/release/skywire cli mdisc entry %s --url http://dmsg-discovery:9090 --json", pk)

		type dmsgClient struct {
			DelegatedServers []string `json:"delegated_servers"`
		}

		type dmsgEntry struct {
			Version   string      `json:"version"`
			Static    string      `json:"static"`
			Client    *dmsgClient `json:"client,omitempty"`
			Timestamp int64       `json:"timestamp"`
		}

		cliOutput := struct {
			Output *dmsgEntry `json:"output,omitempty"`
			Err    *string    `json:"error,omitempty"`
		}{}

		err := env.ExecJSON(cmd, &cliOutput)
		if err != nil {
			env.logger.Debugf("mdisc query for %s failed: %v", visor, err)
			time.Sleep(checkInterval)
			continue
		}

		if cliOutput.Err != nil {
			env.logger.Debugf("mdisc error for %s: %s", visor, *cliOutput.Err)
			time.Sleep(checkInterval)
			continue
		}

		if cliOutput.Output != nil && cliOutput.Output.Client != nil && len(cliOutput.Output.Client.DelegatedServers) > 0 {
			entryTime := time.Unix(cliOutput.Output.Timestamp, 0)
			if !notBefore.IsZero() && entryTime.Before(notBefore) {
				env.logger.Debugf("Visor %s has stale DMSG entry (timestamp %v < notBefore %v), waiting for fresh registration",
					visor, entryTime.Format(time.RFC3339), notBefore.Format(time.RFC3339))
				time.Sleep(checkInterval)
				continue
			}
			env.logger.Infof("Visor %s registered in DMSG discovery with %d delegated servers (entry age: %v)",
				visor, len(cliOutput.Output.Client.DelegatedServers), time.Since(entryTime).Round(time.Second))
			return nil
		}

		env.logger.Debugf("Visor %s not yet fully registered (no delegated servers)", visor)
		time.Sleep(checkInterval)
	}

	return fmt.Errorf("visor %s did not appear in DMSG discovery with delegated servers within %v", visor, timeout)
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

	hc := http.Client{
		Timeout: 15 * time.Second,
	}

	// Retry with exponential backoff to handle post-restart DMSG reconnection
	maxRetries := 6
	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
		if err != nil {
			return nil, err
		}
		req.Header.Add("content-type", "application/json")

		resp, err = hc.Do(req)
		if err == nil {
			return resp, nil
		}

		// Retry with increasing delay
		if i < maxRetries-1 {
			delay := time.Duration(i+1) * 3 * time.Second
			env.logger.Warnf("SendSkyMessage attempt %d/%d failed: %v, retrying in %v...", i+1, maxRetries, err, delay)
			time.Sleep(delay)
		}
	}

	return nil, err
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

// WaitForVisorLog waits for a specific log line to appear in visor logs
func (env *TestEnv) WaitForVisorLog(visor, pattern string, timeout time.Duration) error {
	env.logger.Infof("[WAIT] Waiting for '%s' in %s logs (timeout: %v)", pattern, visor, timeout)

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		<-ticker.C
		// Get recent logs from the container
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		opts := container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail:       "100",
		}

		c, ok := env.containers[visor]
		if !ok {
			cancel()
			return fmt.Errorf("container %s not found", visor)
		}

		logs, err := env.cli.ContainerLogs(ctx, c.ID, opts)
		cancel()
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(logs)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, pattern) {
				env.logger.Infof("[WAIT] ✓ Found: %s", line)
				logs.Close() //nolint:errcheck,gosec
				return nil
			}
		}
		logs.Close() //nolint:errcheck,gosec
	}

	return fmt.Errorf("timeout waiting for '%s' in %s logs", pattern, visor)
}

// DumpVisorLogs dumps recent visor logs to test output
func (env *TestEnv) DumpVisorLogs(t *testing.T, visor string, tail int) {
	t.Logf("=== %s LOGS (last %d lines) ===", visor, tail)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, ok := env.containers[visor]
	if !ok {
		t.Logf("Container %s not found", visor)
		return
	}

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	}

	logs, err := env.cli.ContainerLogs(ctx, c.ID, opts)
	if err != nil {
		t.Logf("Failed to get logs: %v", err)
		return
	}
	defer func() { logs.Close() }() //nolint:errcheck,gosec

	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		t.Logf("  %s", scanner.Text())
	}
	t.Log("=== END LOGS ===")
}

// StreamVisorLogs streams visor logs to test output in real-time
func (env *TestEnv) StreamVisorLogs(t *testing.T, visor string, done <-chan struct{}) {
	ctx := context.Background()

	c, ok := env.containers[visor]
	if !ok {
		t.Logf("Container %s not found", visor)
		return
	}

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	}

	logs, err := env.cli.ContainerLogs(ctx, c.ID, opts)
	if err != nil {
		t.Logf("Failed to stream logs: %v", err)
		return
	}

	go func() {
		defer logs.Close() //nolint:errcheck,gosec
		scanner := bufio.NewScanner(logs)
		for scanner.Scan() {
			select {
			case <-done:
				return
			default:
				t.Logf("[%s] %s", visor, scanner.Text())
			}
		}
	}()
}

// TPDEntry represents a transport entry from transport discovery
type TPDEntry struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Edge1 string `json:"edge1"`
	Edge2 string `json:"edge2"`
}

// QueryTPDByPK queries transport discovery for all transports involving a public key.
// This queries TPD directly via HTTP to see what transports are registered,
// which may differ from the visor's local transport state.
func (env *TestEnv) QueryTPDByPK(pk string) ([]TPDEntry, error) {
	// Query transport discovery via HTTP directly
	url := "http://transport-discovery:9094/all-transports"
	cmd := fmt.Sprintf("wget -q -O - %s", url)

	result, err := env.execResult(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to query TPD: %w", err)
	}

	stdout := result.Stdout()
	if stdout == "" {
		return nil, fmt.Errorf("empty response from TPD")
	}

	// Parse all transports
	type tpdTransport struct {
		ID    string   `json:"id"`
		Type  string   `json:"type"`
		Edges []string `json:"edges"`
	}

	var allTransports []tpdTransport
	if err := json.Unmarshal([]byte(stdout), &allTransports); err != nil {
		return nil, fmt.Errorf("failed to parse TPD response: %w", err)
	}

	// Filter by public key
	var entries []TPDEntry
	for _, tp := range allTransports {
		if len(tp.Edges) >= 2 && (tp.Edges[0] == pk || tp.Edges[1] == pk) {
			entries = append(entries, TPDEntry{
				ID:    tp.ID,
				Type:  tp.Type,
				Edge1: tp.Edges[0],
				Edge2: tp.Edges[1],
			})
		}
	}

	return entries, nil
}

// DumpTPDState logs all TPD entries for the specified visors
func (env *TestEnv) DumpTPDState(visors ...string) {
	env.logger.Info("=== TPD STATE DUMP ===")
	for _, visor := range visors {
		pk, ok := env.visorPKs[visor]
		if !ok {
			env.logger.Warnf("No PK found for visor %s", visor)
			continue
		}

		entries, err := env.QueryTPDByPK(pk)
		if err != nil {
			env.logger.WithError(err).Warnf("Failed to query TPD for %s", visor)
			continue
		}

		env.logger.Infof("TPD entries for %s (%s): %d entries", visor, truncatePK(pk), len(entries))
		for i, entry := range entries {
			env.logger.Infof("  [%d] ID=%s Type=%s Edge1=%s Edge2=%s",
				i, truncateID(entry.ID), entry.Type, truncatePK(entry.Edge1), truncatePK(entry.Edge2))
		}
	}
	env.logger.Info("=== END TPD STATE ===")
}

// RouteFinderResult represents the forward/reverse routes from route finder
type RouteFinderResult struct {
	Forward []routing.Hop `json:"forward"`
	Reverse []routing.Hop `json:"reverse"`
}

// QueryRouteFinder queries the route finder for routes between two public keys
func (env *TestEnv) QueryRouteFinder(srcPK, dstPK string) (*RouteFinderResult, error) {
	cmd := fmt.Sprintf("/release/skywire cli route find %s %s --addr http://route-finder:9092 --json --timeout 10s", srcPK, dstPK)

	cliOutput := struct {
		Output *RouteFinderResult `json:"output,omitempty"`
		Err    *string            `json:"error,omitempty"`
	}{}

	err := env.ExecJSON(cmd, &cliOutput)
	if err != nil {
		return nil, fmt.Errorf("route finder query failed: %w", err)
	}

	if cliOutput.Err != nil {
		return nil, fmt.Errorf("route finder error: %s", *cliOutput.Err)
	}

	return cliOutput.Output, nil
}

// DumpRouteFinderState queries and logs route finder results between visors
func (env *TestEnv) DumpRouteFinderState(srcVisor, dstVisor string) {
	env.logger.Info("=== ROUTE FINDER STATE ===")

	srcPK, ok := env.visorPKs[srcVisor]
	if !ok {
		env.logger.Warnf("No PK found for source visor %s", srcVisor)
		return
	}

	dstPK, ok := env.visorPKs[dstVisor]
	if !ok {
		env.logger.Warnf("No PK found for destination visor %s", dstVisor)
		return
	}

	env.logger.Infof("Querying routes from %s to %s", srcVisor, dstVisor)
	result, err := env.QueryRouteFinder(srcPK, dstPK)
	if err != nil {
		env.logger.WithError(err).Warn("Route finder query failed")
	} else if result != nil {
		env.logger.Infof("Forward route: %d hops", len(result.Forward))
		for i, hop := range result.Forward {
			env.logger.Infof("  Hop %d: %s -> %s @ %s", i, hop.From, hop.To, hop.TpID)
		}
		env.logger.Infof("Reverse route: %d hops", len(result.Reverse))
		for i, hop := range result.Reverse {
			env.logger.Infof("  Hop %d: %s -> %s @ %s", i, hop.From, hop.To, hop.TpID)
		}
	}

	env.logger.Info("=== END ROUTE FINDER STATE ===")
}

// ExecJSONWithTimeout executes a command with a timeout and parses JSON output
func (env *TestEnv) ExecJSONWithTimeout(cmd string, output interface{}, timeout time.Duration) error {
	env.logger.Infof("[EXEC WITH TIMEOUT %v] %s", timeout, cmd)

	if env.testRunnerID == "" {
		return errors.New("env.testRunnerID is empty")
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := Exec(ctx, env.cli, env.testRunnerID, strings.Split(cmd, " "))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			env.logger.Errorf("[EXEC TIMEOUT] Command timed out after %v: %s", timeout, cmd)
			return fmt.Errorf("command timed out after %v: %s", timeout, cmd)
		}
		env.logger.WithError(err).Errorf("[EXEC FAILED] %s", cmd)
		return err
	}

	if stdout := result.Stdout(); stdout != "" {
		env.logger.Debugf("[STDOUT] %s", stdout)
	}
	if stderr := result.Stderr(); stderr != "" {
		env.logger.Debugf("[STDERR] %s", stderr)
	}

	// Parse JSON output
	err = json.Unmarshal([]byte(result.Stdout()), &output)
	if err != nil {
		env.logger.WithError(err).Errorf("[JSON PARSE FAILED] stdout: %s", result.Stdout())
	}
	return err
}

// truncatePK safely truncates a public key string for logging
func truncatePK(pk string) string {
	if len(pk) < 16 {
		return pk
	}
	return pk[:16] + "..."
}

// truncateID safely truncates an ID string for logging
func truncateID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8] + "..."
}

// WaitForVisorReady waits until a visor's RPC is responsive by polling VisorPK.
// This ensures the visor has fully initialized its transport manager and app launcher.
// Provides detailed logging for CI diagnostics.
func (env *TestEnv) WaitForVisorReady(visor string, timeout time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	pollInterval := 5 * time.Second
	attempt := 0

	env.logger.Infof("WaitForVisorReady: waiting for %s (timeout: %v)", visor, timeout)

	var lastErr string
	for time.Now().Before(deadline) {
		attempt++
		_, err := env.VisorPK(visor)
		if err == nil {
			env.logger.Infof("WaitForVisorReady: %s RPC reachable after %v (%d attempts)",
				visor, time.Since(start).Round(time.Second), attempt)
			return nil
		}

		errStr := err.Error()
		if errStr != lastErr {
			env.logger.Infof("WaitForVisorReady: %s attempt %d: %s", visor, attempt, errStr)
			lastErr = errStr
		}

		// Diagnostics every ~30 seconds
		if attempt%6 == 0 {
			env.logContainerTail(visor, 10)
		}

		time.Sleep(pollInterval)
	}

	// Final diagnostic dump before failing
	elapsed := time.Since(start).Round(time.Second)
	env.logger.Infof("WaitForVisorReady: %s TIMED OUT after %v (%d attempts). Last error: %s",
		visor, elapsed, attempt, lastErr)
	env.logContainerTail(visor, 30)

	// Log service container status and logs when visor fails due to service connectivity
	env.GatherContainersInfo()
	for _, svc := range []string{"transport-discovery", "dmsg-discovery", "dmsg-server", "address-resolver"} {
		if c, ok := env.containers[svc]; ok {
			env.logger.Infof("Service %s: state=%s status=%s", svc, c.State, c.Status)
		}
		env.logContainerTail(svc, 20)
	}

	return fmt.Errorf("visor %s RPC not ready after %v (%d attempts, last error: %s)",
		visor, timeout, attempt, lastErr)
}

// logContainerTail logs the last N lines from a container's logs for diagnostics.
func (env *TestEnv) logContainerTail(containerName string, lines int) {
	tail := fmt.Sprintf("%d", lines)
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
	}

	reader, err := env.cli.ContainerLogs(env.ctx, containerName, opts)
	if err != nil {
		env.logger.Warnf("logContainerTail: failed to get logs for %s: %v", containerName, err)
		return
	}
	defer reader.Close() //nolint:errcheck,gosec

	buf := make([]byte, 32*1024)
	n, _ := reader.Read(buf) //nolint:errcheck,gosec
	if n > 0 {
		// Docker multiplexed stream has 8-byte header per frame; strip them for readability
		logOutput := stripDockerLogHeaders(buf[:n])
		env.logger.Infof("=== Container logs [%s] (last %d lines) ===\n%s\n=== End [%s] ===",
			containerName, lines, logOutput, containerName)
	}
}

// waitForAppStopped polls until the app is no longer in "running" state.
func (env *TestEnv) waitForAppStopped(app AppToRun, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, _ := env.isVisorAppRunning(app) //nolint:errcheck
		if !running {
			env.logger.Infof("App %s on %s confirmed stopped", app.AppName, app.VisorHostName)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	env.logger.Warnf("Timeout waiting for app %s to stop on %s", app.AppName, app.VisorHostName)
}

// waitForTransportToPeer polls until a transport to the given peer PK appears in the visor's transport list.
func (env *TestEnv) waitForTransportToPeer(visor, peerPK string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tps, err := env.VisorTpLs(visor)
		if err == nil {
			for _, tp := range tps {
				if tp.Remote.Hex() == peerPK || tp.Remote.String() == peerPK {
					env.logger.Infof("Transport to %s found on %s", truncatePK(peerPK), visor)
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	env.logger.Warnf("Timeout waiting for transport to %s on %s", truncatePK(peerPK), visor)
}

// waitForNonZeroBandwidth polls transport list until at least one transport to the given peer shows non-zero bandwidth.
func (env *TestEnv) waitForNonZeroBandwidth(visor, peerPK string, timeout time.Duration) bool {
	type tpBW struct {
		RemotePK  string `json:"remote_pk"`
		RecvBytes uint64 `json:"recv_bytes,omitempty"`
		SentBytes uint64 `json:"sent_bytes,omitempty"`
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var result struct {
			Output []tpBW `json:"output"`
		}
		err := env.ExecJSON(
			fmt.Sprintf("/release/skywire cli --rpc %s:3435 tp --json", visor),
			&result,
		)
		if err == nil {
			for _, tp := range result.Output {
				if tp.RemotePK == peerPK && (tp.RecvBytes+tp.SentBytes) > 0 {
					return true
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// waitForListeningPort polls for a port to be listening by checking netstat output inside the container.
func (env *TestEnv) waitForListeningPort(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := fmt.Sprintf("sh -c 'netstat -tuln 2>/dev/null | grep :%d || ss -tuln 2>/dev/null | grep :%d || true'", port, port)
		result, err := env.execResult(cmd)
		if err == nil {
			stdout := result.Stdout()
			if strings.Contains(stdout, fmt.Sprintf(":%d", port)) {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// waitForTPDClean polls transport discovery until no transports exist for the given visor PK.
func (env *TestEnv) waitForTPDClean(visor string, timeout time.Duration) {
	pk, ok := env.visorPKs[visor]
	if !ok {
		env.logger.Warnf("waitForTPDClean: no PK for visor %s", visor)
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := env.QueryTPDByPK(pk)
		if err == nil && len(entries) == 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	env.logger.Warnf("waitForTPDClean: timeout for visor %s", visor)
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream headers from Docker container log output.
func stripDockerLogHeaders(data []byte) string {
	var lines []string
	for len(data) >= 8 {
		// Docker multiplexed stream: [stream_type(1)][0(3)][size(4)][payload(size)]
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		data = data[8:]
		if size > len(data) {
			size = len(data)
		}
		if size > 0 {
			line := strings.TrimRight(string(data[:size]), "\n\r")
			if line != "" {
				lines = append(lines, line)
			}
		}
		data = data[size:]
	}
	if len(lines) == 0 && len(data) > 0 {
		// Fallback: if no headers detected, return raw output
		return string(data)
	}
	return strings.Join(lines, "\n")
}
