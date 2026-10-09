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

// TestEnv_EmbeddedServicesCXO: visor-s runs every deployment service in its
// process under the service's own key, as the production hosts do.
// Registration reaches transport-discovery, the address resolver and
// service discovery over CXO, so each one's aggregator must be running and
// subscribed to the visors' feeds.
func TestEnv_EmbeddedServicesCXO(t *testing.T) {
	env := NewEnv().GatherContainersInfo()
	cmd := fmt.Sprintf("/release/skywire cli visor --rpc %s:3435 state --select services --json", visorS)

	want := map[string]string{
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
				if w, wanted := want[a.Name]; wanted && w == svc.Type && a.Error == "" && a.Subscribed > 0 {
					ready++
				}
			}
		}
		return ready == len(want)
	}
	if !assert.Eventually(t, ok, 4*time.Minute, 10*time.Second) {
		t.Fatalf("embedded aggregators on visor-s never all had live subscriptions: %+v", last)
	}
}
