//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
// port (one listener per port, one key) and be subscribed to the other
// visors' feeds. Telemetry and AR binds also take visor-s's own feed
// in-process; its SD feed stays empty because visor-s registers no services.
func TestEnv_EmbeddedServicesCXO(t *testing.T) {
	env := NewEnv().GatherContainersInfo()
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %s:3435 state --select services --json", visorS)

	type aggWant struct {
		svcType    string
		localRoots bool
	}
	want := map[string]aggWant{
		"telemetry": {"transport-discovery", true},
		"ar-bind":   {"address-resolver", true},
		"sd-reg":    {"service-discovery", false},
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
				if w, wanted := want[a.Name]; wanted && w.svcType == svc.Type &&
					a.Error == "" && a.Shared && a.Subscribed > 0 && (!w.localRoots || a.LocalRoots > 0) {
					ready++
				}
			}
		}
		return ready == len(want)
	}
	if !assert.Eventually(t, ok, 4*time.Minute, 10*time.Second) {
		t.Fatalf("embedded aggregators on visor-s never all shared its nodes with live subscriptions: %+v", last)
	}
}
