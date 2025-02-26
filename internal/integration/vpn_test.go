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

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/stretchr/testify/require"
)

const (
	vpnServerTunIPCommand = `ip addr show tun0`
	targetHostScheme      = "https://"
	targetHost            = "google.com"
)

func TestVPN(t *testing.T) {
	tt := []IntegrationTestCase{
		{
			Name: "vpn is functional (DMSG)",
			// this field is needed for the call of `GatherVisorPKs` to get all the needed PKs.
			// but if we refactor code properly, we may do this a middleware before any
			// calls which require PK and remove this field along with the `GatherVisorPKs`.
			// so it's a TODO
			ParticipatingVisorsHostNames: []string{visorVPNClient, visorVPNServer},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorVPNServer,
					AppName:         skyenv.VPNServerName,
					VisorServerName: "",
				},
				{
					VisorHostName:   visorVPNClient,
					AppName:         skyenv.VPNClientName,
					VisorServerName: visorVPNServer,
				},
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorVPNClient,
					ToVisorHostName:   visorVPNServer,
					Type:              "dmsg",
				},
			},
			Case: testVPNIsFunctional,
		},
		{
			Name:                         "vpn can establish STCPR transport",
			ParticipatingVisorsHostNames: []string{visorVPNClient, visorVPNServer},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorVPNServer,
					AppName:         skyenv.VPNServerName,
					VisorServerName: "",
				},
				{
					VisorHostName:   visorVPNClient,
					AppName:         skyenv.VPNClientName,
					VisorServerName: visorVPNServer,
				},
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorVPNClient,
					ToVisorHostName:   visorVPNServer,
					Type:              "stcpr",
				},
			},
			Case: testVPNCanRouteThroughSTCPR,
		},
		{
			Name:                         "vpn can route through SUDPH transport",
			ParticipatingVisorsHostNames: []string{visorVPNClient, visorVPNServer},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorVPNServer,
					AppName:         skyenv.VPNServerName,
					VisorServerName: "",
				},
				{
					VisorHostName:   visorVPNClient,
					AppName:         skyenv.VPNClientName,
					VisorServerName: visorVPNServer,
				},
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorVPNClient,
					ToVisorHostName:   visorVPNServer,
					Type:              "sudph",
				},
			},
			Case: testVPNCanRouteThroughSUDPH,
		},
		{
			Name:                         "simulate vpn server stopped",
			ParticipatingVisorsHostNames: []string{visorVPNClient, visorVPNServer},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorVPNServer,
					AppName:         skyenv.VPNServerName,
					VisorServerName: "",
				},
				{
					VisorHostName:   visorVPNClient,
					AppName:         skyenv.VPNClientName,
					VisorServerName: visorVPNServer,
				},
			},
			AppArgsToSet: []AppArg{
				{
					VisorHostName: visorVPNClient,
					AppName:       skyenv.VPNClientName,
					ArgName:       "killswitch",
					Val:           "true",
				},
			},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorVPNClient,
					ToVisorHostName:   visorVPNServer,
					Type:              network.DMSG,
				},
			},
			Case: testVPNKillServer,
		},
		{
			Name:                         "simulate transport deleted",
			ParticipatingVisorsHostNames: []string{visorVPNClient, visorVPNServer},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorVPNServer,
					AppName:         skyenv.VPNServerName,
					VisorServerName: "",
				},
				{
					VisorHostName:   visorVPNClient,
					AppName:         skyenv.VPNClientName,
					VisorServerName: visorVPNServer,
				},
			},
			AppArgsToSet: []AppArg{},
			TransportsToAdd: []Transport{
				{
					FromVisorHostName: visorVPNClient,
					ToVisorHostName:   visorVPNServer,
					Type:              network.DMSG,
				},
			},
			Case: testVPNRemoveTransport,
		},

		{
			Name:                         "test vpn subcommand list",
			ParticipatingVisorsHostNames: []string{visorVPNServer},
			AppsToRun: []AppToRun{
				{
					VisorHostName:   visorVPNServer,
					AppName:         skyenv.VPNServerName,
					VisorServerName: "",
				},
			},
			AppArgsToSet:    []AppArg{},
			TransportsToAdd: []Transport{},
			Case:            testVPNList,
		},
	}

	RunIntegrationTestCase(t, tt)
}

func testVPNKillServer(t *testing.T, env *TestEnv) {
	serverTUNIP, err := getServerTUNIP(env)
	require.NoError(t, err)
	require.NotEqual(t, "", serverTUNIP)

	// First restart the vpn server
	err = env.ContainerRestart(visorVPNServer)
	require.NoError(t, err)

	// Check client's should not be connected to the vpn anymore / traceroute should not show VPNServer's IP
	firstHop, err := getFirstTracerouteHop(targetHost, env)
	if err != nil {
		require.EqualError(t, err, "no ip found")
	} else {
		require.NotEqual(t, serverTUNIP, firstHop.String())
	}
}

func testVPNRemoveTransport(t *testing.T, env *TestEnv) {
	serverTUNIP, err := getServerTUNIP(env)
	require.NoError(t, err)
	require.NotEqual(t, "", serverTUNIP)

	err = env.VisorRemoveTransport(Transport{
		FromVisorHostName: visorVPNClient,
		ToVisorHostName:   visorVPNServer,
		Type:              network.DMSG,
	})

	require.NoError(t, err)
	firstHop, err := getFirstTracerouteHop(targetHost, env)
	if err != nil {
		require.EqualError(t, err, "no ip found")
	} else {
		require.NotEqual(t, serverTUNIP, firstHop.String())
	}
}

func testVPNList(t *testing.T, env *TestEnv) {
	vpns, err := env.VPNList(visorVPNServer)
	require.NoError(t, err)
	require.Equal(t, env.visorPKs[visorVPNServer], vpns[0].Addr.PubKey().Hex())
}

func testVPNCanRouteThroughSUDPH(t *testing.T, env *TestEnv) {
	t.Run("traffic goes through VPN SUDPH", func(t *testing.T) {
		testTrafficGoesThroughVPN(t, env, targetHost)
	})
}

func testVPNCanRouteThroughSTCPR(t *testing.T, env *TestEnv) {
	t.Run("traffic goes through VPN STCPR", func(t *testing.T) {
		testTrafficGoesThroughVPN(t, env, targetHost)
	})
}

func testVPNIsFunctional(t *testing.T, env *TestEnv) {
	// following tests are based on the definition of functioning VPN:
	// - we can access outer hosts
	// - traffic flows through the VPN server (its TUN)

	t.Run("host is reachable", func(t *testing.T) {
		// google gives 301 as a first code to curl, despite `https` scheme
		testHostIsReachable(t, env, targetHostScheme+targetHost, http.StatusMovedPermanently)
	})

	t.Run("traffic goes through VPN DMSG", func(t *testing.T) {
		testTrafficGoesThroughVPN(t, env, targetHost)
	})
}

func testHostIsReachable(t *testing.T, env *TestEnv, targetURL string, wantRespCode int) {
	code, err := getHTTPRespStatusCodeViaCURLInContainer(env, visorVPNClient, targetURL)
	require.NoError(t, err)
	require.Equal(t, wantRespCode, code)
}

func testTrafficGoesThroughVPN(t *testing.T, env *TestEnv, targetHost string) {
	// basically we have no interface to get real TUN IP on the server side
	// at this moment. also we're running tests separately, not within the server
	// container. but since IP generation is deterministic, we may say exactly
	// which will be the first one generated, if that doesn't change. so,
	serverTUNIP, err := getServerTUNIP(env)
	require.NoError(t, err)
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

	cmdErrC := make(chan error)
	go func() {
		stdout, err = env.ExecInContainerByID(fullCmd, env.containers[visorVPNClient].ID)
		cmdErrC <- err
		close(cmdErrC)
	}()

	// traceroute may hang for really long time, we care about only the first hop,
	// so we give it enough time to get it and interrupt
	time.Sleep(10 * time.Second)
	if err = <-cmdErrC; err != nil {
		return nil, fmt.Errorf("failed to run command %s: %w", fullCmd, err)
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

	// parse output
	outputSplits := strings.Split(output, "\n")
	if len(outputSplits) >= 3 {
		fourthLine := strings.TrimSpace(outputSplits[2])
		serverTUNIP := strings.Split(strings.Split(fourthLine, " ")[1], "/")[0]
		return serverTUNIP, nil
	}

	return "", errors.New("no ip found")
}
