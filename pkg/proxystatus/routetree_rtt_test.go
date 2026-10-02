package proxystatus

import "testing"

// A tunnel's RTT is its slowest live leg's, with the range when legs differ;
// standby legs count only when nothing else is measured.
func TestTunnelRTT(t *testing.T) {
	for _, tc := range []struct {
		legs []Leg
		want string
	}{
		{nil, ""},
		{[]Leg{{Alive: true, RouteLatencyMS: 145}}, "rtt 145ms"},
		{[]Leg{{Alive: true, RouteLatencyMS: 145}, {Alive: true, RouteLatencyMS: 660}}, "rtt 660ms (legs 145–660ms)"},
		{[]Leg{{Alive: true, RouteLatencyMS: 145}, {Alive: true, Standby: true, RouteLatencyMS: 900}}, "rtt 145ms"},
		{[]Leg{{Alive: true, Standby: true, RouteLatencyMS: 300}}, "rtt 300ms"},
		{[]Leg{{Alive: false, RouteLatencyMS: 999}}, ""},
	} {
		if got := tunnelRTT(tc.legs); got != tc.want {
			t.Errorf("tunnelRTT(%+v) = %q, want %q", tc.legs, got, tc.want)
		}
	}
}
