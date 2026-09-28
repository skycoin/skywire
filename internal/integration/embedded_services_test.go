//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// embeddedState is the part of `visor state --select services` this test reads.
type embeddedState struct {
	Services []struct {
		Type        string `json:"type"`
		Running     bool   `json:"running"`
		Error       string `json:"error"`
		Aggregators []struct {
			Name       string `json:"name"`
			Port       uint16 `json:"port"`
			Shared     bool   `json:"shared"`
			Subscribed int    `json:"subscribed"`
			LocalRoots uint64 `json:"local_roots"`
			Error      string `json:"error"`
		} `json:"cxo_aggregators"`
	} `json:"services"`
}

// TestEnv_EmbeddedServicesCXO: visor-s embeds transport-discovery, the
// address resolver and service discovery. Registration reaches them over
// CXO, so each aggregator must run on visor-s's own publisher node for its
// port (one listener per port, one key), be subscribed to the other visors'
// feeds, and take visor-s's own feed in-process.
func TestEnv_EmbeddedServicesCXO(t *testing.T) {
	env := NewEnv().GatherContainersInfo()
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %s:3435 state --select services --json", visorS)

	want := map[string]string{ // aggregator name -> service type
		"telemetry": "transport-discovery",
		"ar-bind":   "address-resolver",
		"sd-reg":    "service-discovery",
	}
	var last embeddedState
	ok := func() bool {
		var st embeddedState
		if err := env.ExecJSON(cmd, &st); err != nil {
			return false
		}
		last = st
		ready := 0
		for _, svc := range st.Services {
			if !svc.Running || svc.Error != "" {
				return false
			}
			for _, a := range svc.Aggregators {
				if typ, wanted := want[a.Name]; wanted && typ == svc.Type &&
					a.Error == "" && a.Shared && a.Subscribed > 0 && a.LocalRoots > 0 {
					ready++
				}
			}
		}
		return ready == len(want)
	}
	require.Eventually(t, ok, 4*time.Minute, 10*time.Second,
		"embedded aggregators on visor-s never all shared its nodes with live subscriptions: %+v", last)
}
