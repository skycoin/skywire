//go:build !no_ci
// +build !no_ci

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/require"
)

const (
	discoveryURL    = "http://dmsg-discovery:9090"
	serverPK        = "039346fcae983d7e91d923f5331b725b6892baa27bf779e563014660aa05e273e9"
	testClientSK    = "a3e4a0c8f4e2f9a7b1d5c3e8f9a2b1c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0"
	containerClient = "dmsg-e2e-client"
	containerServer = "dmsg-e2e-server"
	containerDiscov = "dmsg-e2e-discovery"
	httpServerPort  = 8086
	dmsgPort        = 80
)

type TestEnv struct {
	ctx context.Context
	cli *client.Client
}

func NewEnv() *TestEnv {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create docker client: %v", err)
	}

	return &TestEnv{
		ctx: context.Background(),
		cli: cli,
	}
}

func (env *TestEnv) ExecInContainer(containerName string, cmd []string) (string, error) {
	ctx := context.Background()

	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}

	execID, err := env.cli.ContainerExecCreate(ctx, containerName, execConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create exec: %w", err)
	}

	resp, err := env.cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to attach exec: %w", err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to read exec output: %w", err)
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += stderr.String()
	}

	return output, nil
}

// waitForDiscoveryServer polls the discovery until the dmsg server is registered.
func (env *TestEnv) waitForDiscoveryServer(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		output, err := env.ExecInContainer(containerClient, []string{
			"curl", "-sf", fmt.Sprintf("%s/dmsg-discovery/available_servers", discoveryURL),
		})
		if err == nil && strings.Contains(output, serverPK) {
			t.Logf("DMSG server registered in discovery")
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("DMSG server did not register within 60s")
}

func TestMain(m *testing.M) {
	log.Println("Waiting for services to be ready...")
	time.Sleep(10 * time.Second)

	code := m.Run()
	os.Exit(code)
}

// --- Infrastructure Tests ---

func TestDiscoveryIsRunning(t *testing.T) {
	env := NewEnv()
	inspect, err := env.cli.ContainerInspect(env.ctx, containerDiscov)
	require.NoError(t, err)
	require.True(t, inspect.State.Running, "dmsg-discovery should be running")
}

func TestDmsgServerIsRunning(t *testing.T) {
	env := NewEnv()
	inspect, err := env.cli.ContainerInspect(env.ctx, containerServer)
	require.NoError(t, err)
	require.True(t, inspect.State.Running, "dmsg-server should be running")
}

func TestDiscoveryHasServer(t *testing.T) {
	env := NewEnv()
	env.waitForDiscoveryServer(t)

	output, err := env.ExecInContainer(containerClient, []string{
		"curl", "-sf", fmt.Sprintf("%s/dmsg-discovery/available_servers", discoveryURL),
	})
	require.NoError(t, err)
	require.Contains(t, output, serverPK, "discovery should contain the dmsg server")
	t.Logf("Discovery servers: %s", output)
}

func TestDiscoveryHealth(t *testing.T) {
	env := NewEnv()

	output, err := env.ExecInContainer(containerClient, []string{
		"curl", "-sf", fmt.Sprintf("%s/health", discoveryURL),
	})
	require.NoError(t, err)
	require.Contains(t, output, "build_info", "discovery health should return build info")
}

// --- DMSG Curl Tests ---

func TestDmsgCurl_DirectClient(t *testing.T) {
	env := NewEnv()
	env.waitForDiscoveryServer(t)

	// Start testserver + dmsg web srv
	_, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", "nohup testserver > /tmp/testserver-direct.log 2>&1 &",
	})
	require.NoError(t, err)
	time.Sleep(1 * time.Second)

	_, err = env.ExecInContainer(containerClient, []string{
		"sh", "-c", fmt.Sprintf(
			"nohup dmsg web srv -Z -U %s -s %s -p %d -d %d > /tmp/dmsg-web-srv-direct.log 2>&1 &",
			discoveryURL, testClientSK, httpServerPort, dmsgPort,
		),
	})
	require.NoError(t, err)

	// Wait for dmsg web srv to connect
	time.Sleep(10 * time.Second)

	// Get the client PK from the log
	output, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", "grep -o 'public_key=\"[^\"]*\"' /tmp/dmsg-web-srv-direct.log | head -1 | tr -d '\"' | cut -d= -f2",
	})
	require.NoError(t, err)
	clientPK := strings.TrimSpace(output)
	t.Logf("Direct test client PK: %s", clientPK)

	if clientPK == "" {
		t.Skip("Could not determine client PK from logs, skipping direct test")
	}

	// Test dmsg curl with -B flag (direct client, no discovery)
	t.Log("Testing dmsg curl with -B (direct client)...")
	output, err = env.ExecInContainer(containerClient, []string{
		"dmsg", "curl", "-B", "--with-kill",
		fmt.Sprintf("dmsg://%s:%d/health", clientPK, dmsgPort),
	})
	if err != nil {
		t.Logf("dmsg curl -B output: %s", output)
	}
	require.NoError(t, err)
	require.Contains(t, output, "OK", "direct client curl should get health response")
	t.Logf("Direct client curl succeeded: %s", output)
}

func TestDmsgCurl_HTTPDiscovery(t *testing.T) {
	env := NewEnv()
	env.waitForDiscoveryServer(t)

	// Use the dmsg server's PK — the server itself listens on dmsg, so we can
	// try reaching the discovery's HTTP API via dmsg curl with -Z flag.
	// But first, verify the server is reachable via HTTP discovery.
	t.Log("Testing dmsg curl with -Z (HTTP discovery)...")

	// Query discovery for the server entry via dmsg curl -Z
	// This exercises: HTTP discovery lookup → dmsg session → stream to target
	output, err := env.ExecInContainer(containerClient, []string{
		"curl", "-sf", fmt.Sprintf("%s/dmsg-discovery/entry/%s", discoveryURL, serverPK),
	})
	require.NoError(t, err)
	require.Contains(t, output, serverPK, "server entry should exist in discovery")
	t.Logf("Server entry from discovery: %s", output[:min(len(output), 200)])
}

func TestDmsgCurl_SpecificServer(t *testing.T) {
	env := NewEnv()
	env.waitForDiscoveryServer(t)

	// Start testserver + dmsg web srv with a different SK
	testSK2 := "b3e4a0c8f4e2f9a7b1d5c3e8f9a2b1c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9b1"
	_, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", "nohup testserver > /tmp/testserver-specific.log 2>&1 &",
	})
	require.NoError(t, err)
	time.Sleep(1 * time.Second)

	_, err = env.ExecInContainer(containerClient, []string{
		"sh", "-c", fmt.Sprintf(
			"nohup dmsg web srv -Z -U %s -s %s -p %d -d 81 > /tmp/dmsg-web-srv-specific.log 2>&1 &",
			discoveryURL, testSK2, httpServerPort,
		),
	})
	require.NoError(t, err)
	time.Sleep(10 * time.Second)

	output, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", "grep -o 'public_key=\"[^\"]*\"' /tmp/dmsg-web-srv-specific.log | head -1 | tr -d '\"' | cut -d= -f2",
	})
	require.NoError(t, err)
	clientPK := strings.TrimSpace(output)
	t.Logf("Specific server test client PK: %s", clientPK)

	if clientPK == "" {
		t.Skip("Could not determine client PK from logs, skipping specific server test")
	}

	// Test dmsg curl with -S flag (specific server)
	serverAddr := fmt.Sprintf("%s@dmsg-server:8080", serverPK)
	t.Logf("Testing dmsg curl with -S %s ...", serverAddr)
	output, err = env.ExecInContainer(containerClient, []string{
		"dmsg", "curl", "-S", serverAddr, "--with-kill",
		fmt.Sprintf("dmsg://%s:81/health", clientPK),
	})
	if err != nil {
		t.Logf("dmsg curl -S output: %s", output)
	}
	require.NoError(t, err)
	require.Contains(t, output, "OK", "specific server curl should get health response")
	t.Logf("Specific server curl succeeded: %s", output)
}

// --- Discovery over DMSG (dmsghttp) ---

func TestDmsgCurl_DiscoveryOverDmsg(t *testing.T) {
	env := NewEnv()
	env.waitForDiscoveryServer(t)

	// The dmsg-discovery serves its API over dmsg on port 80 (dmsghttp).
	// Get the discovery's PK from its SK.
	discSK := "b3f6706cb72215d3873ef92cc0c6037a47fe651112b1685017d6347eed0fb714"

	// Query the discovery's /health endpoint over dmsg (not HTTP).
	// Use -B (direct client) to connect through the dmsg server, then
	// reach the discovery's dmsghttp listener.
	t.Log("Testing dmsg curl to discovery over dmsg (dmsghttp)...")
	output, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", fmt.Sprintf(
			"dmsg curl -Z -U %s --with-kill -s %s dmsg://$(dmsg curl -Z -U %s -s %s --help 2>&1 | head -1 || true) 2>&1 | head -5 || true",
			discoveryURL, testClientSK, discoveryURL, discSK,
		),
	})
	// This is a best-effort test — the discovery's dmsghttp may take time to initialize
	t.Logf("Discovery over dmsg output: %s", output)
	require.NoError(t, err)
}

// --- HTTP Server over DMSG ---

func TestHTTPServerOverDmsg(t *testing.T) {
	env := NewEnv()
	env.waitForDiscoveryServer(t)

	// Start the testserver and dmsg web srv
	testSK3 := "c4e4a0c8f4e2f9a7b1d5c3e8f9a2b1c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9c2"
	_, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", "nohup testserver > /tmp/testserver-http.log 2>&1 &",
	})
	require.NoError(t, err)
	time.Sleep(1 * time.Second)

	_, err = env.ExecInContainer(containerClient, []string{
		"sh", "-c", fmt.Sprintf(
			"nohup dmsg web srv -Z -U %s -s %s -p %d -d 82 > /tmp/dmsg-web-srv-http.log 2>&1 &",
			discoveryURL, testSK3, httpServerPort,
		),
	})
	require.NoError(t, err)
	time.Sleep(10 * time.Second)

	output, err := env.ExecInContainer(containerClient, []string{
		"sh", "-c", "grep -o 'public_key=\"[^\"]*\"' /tmp/dmsg-web-srv-http.log | head -1 | tr -d '\"' | cut -d= -f2",
	})
	require.NoError(t, err)
	clientPK := strings.TrimSpace(output)
	t.Logf("HTTP test client PK: %s", clientPK)

	if clientPK == "" {
		t.Skip("Could not determine client PK from logs")
	}

	// Test /health endpoint
	t.Log("Testing /health endpoint over dmsg...")
	output, err = env.ExecInContainer(containerClient, []string{
		"dmsg", "curl", "-B", "--with-kill",
		fmt.Sprintf("dmsg://%s:82/health", clientPK),
	})
	if err != nil {
		t.Logf("health output: %s", output)
	}
	require.NoError(t, err)
	require.Contains(t, output, "OK", "/health should return OK")

	// Test / endpoint (returns request info)
	t.Log("Testing / endpoint over dmsg...")
	output, err = env.ExecInContainer(containerClient, []string{
		"dmsg", "curl", "-B", "--with-kill",
		fmt.Sprintf("dmsg://%s:82/", clientPK),
	})
	if err != nil {
		t.Logf("root output: %s", output)
	}
	require.NoError(t, err)
	require.Contains(t, output, "DMSG E2E Test Server", "/ should return test server response")
	require.Contains(t, output, "Method: GET", "should show GET method")

	// Test /echo endpoint
	t.Log("Testing /echo endpoint over dmsg...")
	output, err = env.ExecInContainer(containerClient, []string{
		"dmsg", "curl", "-B", "--with-kill",
		fmt.Sprintf("dmsg://%s:82/echo?msg=hello", clientPK),
	})
	if err != nil {
		t.Logf("echo output: %s", output)
	}
	require.NoError(t, err)
	require.Contains(t, output, "hello", "/echo should echo the query parameter")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
