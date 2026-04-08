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
	if err != nil {
		t.Fatalf("SendSkyMessage failed: %v", err)
	}
	if res == nil {
		t.Fatal("SendSkyMessage returned nil response")
	}
	defer res.Body.Close() //nolint:errcheck
	require.Equal(t, 200, res.StatusCode, "expected HTTP 200 from skychat")
}
