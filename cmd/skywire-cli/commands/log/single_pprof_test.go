package clilog

import "testing"

func TestPprofPath(t *testing.T) {
	tests := []struct {
		profile string
		sec     int
		debug   int
		gc      bool
		want    string
	}{
		{"heap", 0, 0, false, "/debug/pprof/heap"},
		{"profile", 10, 0, false, "/debug/pprof/profile?seconds=10"},
		{"goroutine", 0, 2, false, "/debug/pprof/goroutine?debug=2"},
		{"heap", 0, 1, true, "/debug/pprof/heap?debug=1&gc=1"},
		{"heap", 5, 1, true, "/debug/pprof/heap?debug=1&gc=1&seconds=5"},
	}
	for _, tc := range tests {
		if got := pprofPath(tc.profile, tc.sec, tc.debug, tc.gc); got != tc.want {
			t.Errorf("pprofPath(%q,%d,%d,%v) = %q, want %q", tc.profile, tc.sec, tc.debug, tc.gc, got, tc.want)
		}
	}
}
