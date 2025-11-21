//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessagingWithRestarts(t *testing.T) {
	const routerVisor = visorB

	skychatVisors := []string{visorA, visorC}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	res, err := env.SendSkyMessage(visorA, visorC, visorA+" -> "+visorC)
	require.NoError(t, err)

	require.NoError(t, res.Body.Close())
}
