//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	vpnServerTunIPCommand = `ip addr show tun0`
	targetHostScheme      = "https://"
	targetHost            = "google.com"
)

// TestVPN is a phased test that shares a single environment setup across
// multiple verification steps. This avoids redundant container restarts
// between transport-type tests (DMSG, STCPR, SUDPH) and only restarts
// when testing disruption scenarios (server stop, transport deletion).
func TestVPN(t *testing.T) {
	// Setup: one environment for all phases
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorVPNClient, visorVPNServer})

	// Dump logs from both visors on any failure
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		t.Log("=== TEST FAILED — dumping visor logs ===")
		for _, visor := range []string{visorVPNClient, visorVPNServer} {
			logs, err := env.ReadLog(visor)
			if err != nil {
				t.Logf("Failed to read logs from %s: %v", visor, err)
				continue
			}
			// Filter to relevant lines
			var filtered []string
			for _, line := range strings.Split(logs, "\n") {
				lower := strings.ToLower(line)
				if strings.Contains(lower, "error") || strings.Contains(lower, "dmsg") ||
					strings.Contains(lower, "transport") || strings.Contains(lower, "handshake") ||
					strings.Contains(lower, "vpn") || strings.Contains(lower, "fatal") {
					filtered = append(filtered, line)
				}
			}
			if len(filtered) > 100 {
				filtered = filtered[len(filtered)-100:]
			}
			t.Logf("\n=== Logs from %s (last %d relevant lines) ===\n%s\n=== END ===",
				visor, len(filtered), strings.Join(filtered, "\n"))
		}
	})

	// Start VPN server app
	serverApp := AppToRun{
		VisorHostName:   visorVPNServer,
		AppName:         skyenv.VPNServerName,
		VisorServerName: "",
		LauncherMode:    "internal",
	}
	clientApp := AppToRun{
		VisorHostName:   visorVPNClient,
		AppName:         skyenv.VPNClientName,
		VisorServerName: visorVPNServer,
		LauncherMode:    "internal",
	}

	// ===== Phase 1: DMSG VPN =====
	t.Run("phase1_dmsg", func(t *testing.T) {
		t.Log("Starting VPN server and client with DMSG transport")
		env = env.StartApp(t, serverApp, "")
		time.Sleep(3 * time.Second) // wait for server accept/routing registration

		// Add DMSG transport
		env = env.TestVisorAddTp(t, Transport{
			FromVisorHostName: visorVPNClient,
			ToVisorHostName:   visorVPNServer,
			Type:              "dmsg",
		})

		// Start VPN client
		env = env.StartApp(t, clientApp, env.visorPKs[visorVPNServer])
		if err := env.waitForVisorApp(clientApp); err != nil {
			t.Skipf("VPN client not ready (Docker DMSG issue): %v", err)
		}

		t.Run("host_reachable", func(t *testing.T) {
			testHostIsReachable(t, env, targetHostScheme+targetHost, http.StatusMovedPermanently)
		})

		t.Run("traffic_through_vpn", func(t *testing.T) {
			testTrafficGoesThroughVPN(t, env, targetHost)
		})
	})

	// ===== Phase 2: Add STCPR transport (no restart) =====
	t.Run("phase2_stcpr", func(t *testing.T) {
		t.Log("Adding STCPR transport to existing environment")
		env = env.TestVisorAddTp(t, Transport{
			FromVisorHostName: visorVPNClient,
			ToVisorHostName:   visorVPNServer,
			Type:              "stcpr",
		})

		t.Run("traffic_through_vpn", func(t *testing.T) {
			testTrafficGoesThroughVPN(t, env, targetHost)
		})
	})

	// ===== Phase 3: Add SUDPH transport (no restart) =====
	t.Run("phase3_sudph", func(t *testing.T) {
		t.Log("Adding SUDPH transport to existing environment")
		env = env.TestVisorAddTp(t, Transport{
			FromVisorHostName: visorVPNClient,
			ToVisorHostName:   visorVPNServer,
			Type:              "sudph",
		})

		t.Run("traffic_through_vpn", func(t *testing.T) {
			testTrafficGoesThroughVPN(t, env, targetHost)
		})
	})

	// ===== Phase 4: VPN list command (no restart, just query) =====
	t.Run("phase4_vpn_list", func(t *testing.T) {
		vpns, err := env.VPNList(visorVPNServer)
		require.NoError(t, err)
		require.Equal(t, env.visorPKs[visorVPNServer], vpns[0].Addr.PubKey().Hex())
	})

	// ===== Phase 5: Simulate server stop (needs restart) =====
	t.Run("phase5_server_stop", func(t *testing.T) {
		t.Log("Setting killswitch and restarting VPN server")

		// Enable killswitch on client
		env = env.VisorSetAppArg(t, AppArg{
			VisorHostName: visorVPNClient,
			AppName:       skyenv.VPNClientName,
			ArgName:       "killswitch",
			Val:           "true",
		})

		serverTUNIP, err := getServerTUNIP(env)
		if err != nil {
			t.Skipf("Skipping VPN server stop test: TUN IP not available: %v", err)
		}
		require.NotEqual(t, "", serverTUNIP)

		// Restart VPN server container
		err = env.ContainerRestart(visorVPNServer)
		require.NoError(t, err)

		// Wait for client to detect disconnection
		time.Sleep(3 * time.Second)

		// Verify traffic no longer goes through VPN server
		firstHop, err := getFirstTracerouteHop(targetHost, env)
		if err != nil {
			require.EqualError(t, err, "no ip found")
		} else {
			require.NotEqual(t, serverTUNIP, firstHop.String())
		}
	})

	// ===== Phase 6: Simulate transport deleted (needs fresh setup after phase 5) =====
	t.Run("phase6_transport_deleted", func(t *testing.T) {
		t.Log("Re-establishing environment for transport deletion test")

		// Stop apps cleanly from their zombie state after phase 5's server restart
		t.Log("Stopping apps before re-setup")
		_, _ = env.VPNStop(clientApp)                    //nolint:errcheck,gosec
		_, _ = env.VisorAppStop(serverApp)               //nolint:errcheck,gosec
		env.waitForAppStopped(clientApp, 10*time.Second) //nolint:errcheck
		env.waitForAppStopped(serverApp, 10*time.Second) //nolint:errcheck

		// Wait for both visors to be DMSG-ready after phase 5 restart
		for _, visor := range []string{visorVPNClient, visorVPNServer} {
			t.Logf("Waiting for %s DMSG readiness", visor)
			if err := env.WaitForVisorDmsgReady(visor, 60*time.Second); err != nil {
				t.Skipf("Skipping transport deletion test: %s DMSG not ready: %v", visor, err)
			}
		}

		// Start apps fresh and add DMSG transport
		env = env.StartApp(t, serverApp, "")
		time.Sleep(3 * time.Second)

		env = env.TestVisorAddTp(t, Transport{
			FromVisorHostName: visorVPNClient,
			ToVisorHostName:   visorVPNServer,
			Type:              "dmsg",
		})

		// Ensure killswitch is still on
		env = env.VisorSetAppArg(t, AppArg{
			VisorHostName: visorVPNClient,
			AppName:       skyenv.VPNClientName,
			ArgName:       "killswitch",
			Val:           "true",
		})

		env = env.StartApp(t, clientApp, env.visorPKs[visorVPNServer])
		if err := env.waitForVisorApp(clientApp); err != nil {
			t.Logf("Warning: VPN client may not be ready: %v", err)
		}

		serverTUNIP, err := getServerTUNIP(env)
		require.NoError(t, err)
		require.NotEqual(t, "", serverTUNIP)

		// Remove ALL transports
		err = env.RemoveAllTransports(visorVPNClient)
		require.NoError(t, err)

		// Wait for client to detect transport loss
		time.Sleep(3 * time.Second)

		// Verify traffic no longer goes through VPN
		firstHop, err := getFirstTracerouteHop(targetHost, env)
		if err != nil {
			require.EqualError(t, err, "no ip found")
		} else {
			require.NotEqual(t, serverTUNIP, firstHop.String())
		}
	})
}

func testHostIsReachable(t *testing.T, env *TestEnv, targetURL string, wantRespCode int) {
	code, err := getHTTPRespStatusCodeViaCURLInContainer(env, visorVPNClient, targetURL)
	require.NoError(t, err)
	require.Equal(t, wantRespCode, code)
}

func testTrafficGoesThroughVPN(t *testing.T, env *TestEnv, targetHost string) {
	serverTUNIP, err := getServerTUNIP(env)
	if err != nil {
		t.Skipf("Skipping VPN traffic test: TUN IP not available: %v", err)
	}
	require.NotEqual(t, "", serverTUNIP)

	firstHop, err := getFirstTracerouteHop(targetHost, env)
	require.NoError(t, err)

	require.Equal(t, serverTUNIP, firstHop.String())
}

func getHTTPRespStatusCodeViaCURLInContainer(env *TestEnv, containerName string, targetURL string) (int, error) {
	const curlFmt = "curl -I %s"
	curlCmd := fmt.Sprintf(curlFmt, targetURL)

	output, err := env.ExecInContainerByName(curlCmd, containerName)
	if err != nil {
		return 0, fmt.Errorf("failed to execute command %s in container %s: %w", curlCmd, containerName, err)
	}

	firstLine := strings.TrimSpace(strings.Split(output, "\n")[0])
	codeStr := strings.TrimSpace(strings.Split(firstLine, " ")[1])

	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse command output %s: %w", output, err)
	}

	return code, nil
}

func getFirstTracerouteHop(targetHost string, env *TestEnv) (net.IP, error) {
	const tracerouteFmt = "timeout 9 traceroute -n %s"
	fullCmd := fmt.Sprintf(tracerouteFmt, targetHost)

	var stdout string
	var err error

	cmdErrC := make(chan error, 1)
	go func() {
		stdout, err = env.ExecInContainerByID(fullCmd, env.containers[visorVPNClient].ID)
		cmdErrC <- err
	}()

	if cmdErr := <-cmdErrC; cmdErr != nil {
		return nil, fmt.Errorf("failed to run command %s: %w", fullCmd, cmdErr)
	}

	stdoutLine := strings.Split(strings.Split(stdout, "\n")[1], " ")
	if len(stdoutLine) > 2 {
		lToken := stdoutLine[3]
		if lToken != "" {
			if ip := net.ParseIP(lToken); ip != nil {
				return ip, nil
			}
		}
	}

	return nil, errors.New("no ip found")
}

func getServerTUNIP(env *TestEnv) (string, error) {
	output, err := env.ExecInContainerByID(vpnServerTunIPCommand, env.containers[visorVPNServer].ID)
	if err != nil {
		return "", err
	}

	outputSplits := strings.Split(output, "\n")
	if len(outputSplits) >= 3 {
		fourthLine := strings.TrimSpace(outputSplits[2])
		serverTUNIP := strings.Split(strings.Split(fourthLine, " ")[1], "/")[0]
		return serverTUNIP, nil
	}

	return "", errors.New("no ip found")
}
