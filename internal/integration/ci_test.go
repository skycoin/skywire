//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/stretchr/testify/require"
)

// TODO: implement TestEnv.startup in code (without need for docker-compose up)
// TODO: implement TestEnv.teardown(), TestEnv.restart()

const (
	// testURLLAN     = "http://dmsg-discovery:9090/dmsg-discovery/available_servers"
	testURLWAN     = "https://www.google.com"
	visorA         = "visor-a"
	visorB         = "visor-b"
	visorC         = "visor-c"
	visorVPNServer = visorA
	visorVPNClient = visorC
	statusRunning  = swarm.TaskStateRunning
)

// nolint:gochecknoglobals
var (
	RestartDelay = time.Second * 25
	HTTPTimeout  = time.Second * 5
	HTTPGetDelay = time.Millisecond
)

func TestMain(m *testing.M) {
	testLogLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(testLogLevel)
		if err != nil {
			log.Fatal(err)
		}

		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	if delay, ok := os.LookupEnv("RESTART_DELAY"); ok {
		if parsed, err := time.ParseDuration(delay); err == nil {
			log.Printf("RESTART_DELAY set to: %v", parsed)
			RestartDelay = parsed
		} else {
			log.Printf("Parse error of RESTART_DELAY: %v", err)
		}
	} else {
		log.Printf("RESTART_DELAY not set. Using value: %v", RestartDelay)
	}

	code := m.Run()

	os.Exit(code)
}

func TestWAN(t *testing.T) {
	client := &http.Client{
		Timeout: HTTPTimeout,
	}

	res, err := client.Get(testURLWAN)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, res.Body.Close())
}

func TestNewEnv(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	runningContainers := map[string]string{}

	for _, container := range env.containers {
		runningContainers[container.Names[0]] = container.State
	}

	for _, n := range env.visorNames {
		require.EqualValues(t, statusRunning, runningContainers[n])
	}

	for _, n := range env.serviceNames {
		require.EqualValues(t, statusRunning, runningContainers[n])
	}
}

func TestEnv_cli(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	containersIPs := map[string]string{}

	for _, container := range env.containers {
		c, err := env.cli.ContainerInspect(env.ctx, container.ID)
		if err != nil {
			return
		}

		network, ok := c.NetworkSettings.Networks[env.intraNet]
		if ok {
			containersIPs[container.Names[0]] = network.IPAddress
		}
	}

	cases := []struct {
		Name string
		IP   string
	}{
		{
			Name: "/" + visorA,
			IP:   "174.0.0.11",
		},
		{
			Name: "/" + visorB,
			IP:   "174.0.0.12",
		},
		{
			Name: "/" + visorC,
			IP:   "174.0.0.13",
		},
	}

	for _, v := range cases {
		require.Equal(t, v.IP, containersIPs[v.Name])
	}
}

func TestEnv_VisorAppLs(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	output, err := env.VisorAppLs(visorB)
	require.NoError(t, err)
	require.Equal(t, 2, len(output))
}

func TestEnv_VisorPK(t *testing.T) {
	env := NewEnv().GatherContainersInfo()

	visorsPKs := map[string]string{}

	for _, visor := range []string{visorA, visorB, visorC} {
		pk, err := env.VisorPK(visor)
		require.NoError(t, err)

		visorsPKs[visor] = pk
	}
}

func TestEnv_VisorAddTp(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	pkA := env.visorPKs[visorA]

	out, err := env.VisorTpAddDefault(visorB, pkA)
	require.NoError(t, err)
	require.Contains(t, out.Remote.Hex(), pkA)
	rmOut, err := env.VisorTpRm(visorB, out.ID)
	require.NoError(t, err)
	require.Equal(t, "OK", rmOut)
}

func TestEnv_VisorAddTp_second(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	for _, visor := range []string{visorA, visorC} {
		pk := env.visorPKs[visor]

		out, err := env.VisorTpAddDefault(visorB, pk)
		require.NoError(t, err)
		require.Contains(t, out.Remote.Hex(), pk)

		rmOut, err := env.VisorTpRm(visorB, out.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmOut)
	}
}

func TestEnv_SendSkyMessage(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorC}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	_, err := env.SendSkyMessage(visorA, visorC, visorA+" -> "+visorC)
	require.NoError(t, err)
}

func TestEnv_SendSkyMessage_second(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorC}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	// For reasons unknown atm with qty big enough messaging FAILs
	// TODO: Parametrize qty, find value on which messaging FAILs, detect the cause
	const (
		qty       = 32
		doubleQty = 2 * qty
	)

	errCh := make(chan error, doubleQty)

	sendMessage := func(idx int, sender, recipient string) error {
		msg := fmt.Sprintf("Msg: %v. From %v to %v", idx, sender, recipient)

		res, err := env.SendSkyMessage(sender, recipient, msg)
		if err != nil {
			return err
		}

		require.NoError(t, res.Body.Close())

		return nil
	}

	for i := 0; i < qty; i++ {
		errCh <- sendMessage(i, visorA, visorC)
		errCh <- sendMessage(i, visorC, visorA)
	}

	close(errCh)

	var idx int
	for err := range errCh {
		idx++

		require.NoError(t, err)
	}

	require.EqualValues(t, doubleQty, idx)
}

func TestEnv_ContainerRestart(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorC}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	require.NoError(t, env.ContainerRestart(visorB))
}

func TestEnv_ReadLog(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorB}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	logData, err := env.ReadLog(visorB)
	require.NoError(t, err)
	require.Greater(t, len(logData), 0)
}

func TestEnv_RmTp(t *testing.T) {
	routerVisor := visorB
	skychatVisors := []string{visorA, visorB}
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	tps, err := env.VisorTpLs(visorB)
	require.NoError(t, err)
	for _, tp := range tps {
		rmTpSum, err := env.VisorTpRm(visorB, tp.ID)
		require.NoError(t, err)
		require.Equal(t, "OK", rmTpSum)
	}
}

func TestEnv_Tp(t *testing.T) {
	env := NewEnv().GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	for _, visor := range []string{visorA, visorC} {
		pk := env.visorPKs[visor]

		tpTypes, err := env.VisorTpType(visorB)
		require.NoError(t, err)
		for _, tpType := range tpTypes {
			if tpType != network.STCP {
				addTpSum, err := env.VisorTpAdd(visorB, pk, tpType)
				require.NoError(t, err)
				require.Contains(t, addTpSum.Remote.Hex(), pk)

				tpSum, err := env.VisorTpID(visorB, addTpSum.ID)
				require.NoError(t, err)
				require.Contains(t, tpSum.Remote.Hex(), pk)

				rmTpSum, err := env.VisorTpRm(visorB, addTpSum.ID)
				require.NoError(t, err)
				require.Equal(t, "OK", rmTpSum)
			}
		}

	}
}

// func TestEnv_Route(t *testing.T) {
// 	env := NewEnv().GatherContainersInfo().
// 		GatherVisorPKs([]string{visorA, visorB, visorC})

// 	rules, err := env.VisorRouteLsRules(visorA)
// 	require.NoError(t, err)
// 	var routeID routing.RouteID
// 	routeID = 0
// 	for _, rule := range rules {
// 		if routeID < rule.ID {
// 			routeID = rule.ID
// 		}
// 	}
// 	routeID = routeID + 1
// 	localPK := env.visorPKs[visorA]
// 	localPort := "1"

// 	remotePK := env.visorPKs[visorB]
// 	remotePort := "2"

// 	appRKey, err := env.VisorRouteAddAppRule(visorA, fmt.Sprint(routeID), localPK, localPort, remotePK, remotePort)
// 	require.NoError(t, err)

// 	appRRule, err := env.VisorRouteRule(visorA, appRKey.RoutingRuleKey)
// 	require.NoError(t, err)
// 	require.Equal(t, "Consume", appRRule.Type)
// 	require.Equal(t, localPort, appRRule.LocalPort)
// 	require.Equal(t, remotePK, appRRule.RemotePK)
// 	require.Equal(t, remotePort, appRRule.RemotePort)

// 	out, err := env.VisorRouteRmRule(visorA, appRRule.ID)
// 	require.NoError(t, err)
// 	require.Equal(t, "OK", out)

// 	fwdNextTpID := uuid.New()

// 	fwdRKey, err := env.VisorRouteAddFwdRule(visorA, fmt.Sprint(routeID+1), fmt.Sprint(routeID+1), fwdNextTpID.String(), localPK, localPort, remotePK, remotePort)
// 	require.NoError(t, err)

// 	fwdRRule, err := env.VisorRouteRule(visorA, fwdRKey.RoutingRuleKey)
// 	require.NoError(t, err)
// 	require.Equal(t, routeID+1, fwdRRule.ID)
// 	require.Equal(t, "Forward", fwdRRule.Type)
// 	require.Equal(t, fmt.Sprint(routeID+1), fwdRRule.NextRouteID)
// 	require.Equal(t, fwdNextTpID.String(), fwdRRule.NextTpID)
// 	require.Equal(t, localPort, appRRule.LocalPort)
// 	require.Equal(t, remotePK, appRRule.RemotePK)
// 	require.Equal(t, remotePort, appRRule.RemotePort)

// 	intFwdNextTpID := uuid.New()
// 	intFwdRKey, err := env.VisorRouteAddIntFwdRule(visorA, fmt.Sprint(routeID+2), fmt.Sprint(routeID+2), intFwdNextTpID.String())
// 	require.NoError(t, err)
// 	intFwdRRule, err := env.VisorRouteRule(visorA, intFwdRKey.RoutingRuleKey)
// 	require.NoError(t, err)
// 	require.Equal(t, routeID+2, intFwdRRule.ID)
// 	require.Equal(t, "IntermediaryForward", intFwdRRule.Type)
// 	require.Equal(t, fmt.Sprint(routeID+2), intFwdRRule.NextRouteID)
// 	require.Equal(t, intFwdNextTpID.String(), intFwdRRule.NextTpID)
// }
