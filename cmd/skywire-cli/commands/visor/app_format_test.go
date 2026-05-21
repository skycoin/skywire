// Package visor cmd/skywire-cli/commands/visor/app_format_test.go
//
// Pins the formatAppList AppStatus → display-string mapping. Pre-fix
// the switch had cases only for Running and Errored, so an app in
// AppStatusStarting state rendered as the default "stopped" — the
// operator-visible symptom that surfaced during the 2026-05-21
// skynet port-forwarding investigation as `cli visor app ls` showing
// "stopped" rows whose DetailedStatus was "Starting", inconsistent
// and confusing.

package clivisor

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

func TestFormatAppList_RendersEveryAppStatus(t *testing.T) {
	cases := []struct {
		status     appserver.AppStatus
		wantStatus string
	}{
		{appserver.AppStatusStopped, "stopped"},
		{appserver.AppStatusRunning, "running"},
		{appserver.AppStatusErrored, "errored"},
		{appserver.AppStatusStarting, "starting"},
	}

	for _, tc := range cases {
		t.Run(tc.wantStatus, func(t *testing.T) {
			states := []*appserver.AppState{
				{
					AppConfig: appserver.AppConfig{
						Name: "test-app",
						Port: 42,
					},
					Status:         tc.status,
					DetailedStatus: "any-detail",
				},
			}

			rows, text := formatAppList(states)
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(rows))
			}
			if rows[0].Status != tc.wantStatus {
				t.Errorf("Status=%q, want %q", rows[0].Status, tc.wantStatus)
			}
			if !strings.Contains(text, tc.wantStatus) {
				t.Errorf("text output missing %q; got:\n%s", tc.wantStatus, text)
			}
		})
	}
}

func TestFormatAppList_UnknownStatusFallsBackToStopped(t *testing.T) {
	// An unrecognized AppStatus (e.g., a future variant that hasn't
	// been added to the switch yet) should fall back to "stopped" —
	// safer default than blowing up. Pin the fallback so a future
	// refactor doesn't accidentally panic on an unknown int.
	states := []*appserver.AppState{
		{
			AppConfig: appserver.AppConfig{Name: "test", Port: 1},
			Status:    appserver.AppStatus(99), // bogus
		},
	}
	rows, _ := formatAppList(states)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "stopped" {
		t.Errorf("unknown status: rendered %q, want fallback %q", rows[0].Status, "stopped")
	}
}
