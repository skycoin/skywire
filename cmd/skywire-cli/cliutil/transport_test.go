// Package cliutil cmd/skywire-cli/cliutil/transport_test.go c4-vis-cli
package cliutil

import "testing"

func TestSplitTargetScheme(t *testing.T) {
	const pk = "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"
	cases := []struct {
		in         string
		wantScheme string
		wantRest   string
	}{
		{pk, "", pk}, // bare PK unchanged
		{pk + ":remote.bin", "", pk + ":remote.bin"},                   // bare pk:path unchanged
		{pk + "@1.2.3.4:2022", "", pk + "@1.2.3.4:2022"},               // bare pk@host:port unchanged
		{"dmsg://" + pk, "dmsg", pk},                                   // dmsg scheme stripped
		{"skynet://" + pk + ":f", "skynet", pk + ":f"},                 // skynet scheme + path
		{"tcp://" + pk + "@1.2.3.4:2022", "tcp", pk + "@1.2.3.4:2022"}, // tcp scheme stripped
		{"./local/path", "", "./local/path"},                           // local path unchanged
		{"http://example", "", "http://example"},                       // unrecognized scheme untouched
	}
	for _, c := range cases {
		gotScheme, gotRest := SplitTargetScheme(c.in)
		if gotScheme != c.wantScheme || gotRest != c.wantRest {
			t.Errorf("SplitTargetScheme(%q) = (%q,%q); want (%q,%q)", c.in, gotScheme, gotRest, c.wantScheme, c.wantRest)
		}
	}
}

func TestResolveTransport(t *testing.T) {
	cases := []struct {
		name         string
		flagVal      string
		flagChanged  bool
		targetScheme string
		want         string
		wantErr      bool
	}{
		{"default auto, bare target", "auto", false, "", "auto", false},
		{"empty flag treated as auto", "", false, "", "auto", false},
		{"explicit dmsg, bare target", "dmsg", true, "", "dmsg", false},
		{"only target scheme", "auto", false, "skynet", "skynet", false},
		{"flag + target agree", "dmsg", true, "dmsg", "dmsg", false},
		{"flag auto yields to target scheme", "auto", true, "tcp", "tcp", false},
		{"conflict errors", "dmsg", true, "skynet", "", true},
		{"unchanged flag never conflicts", "dmsg", false, "skynet", "skynet", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveTransport(c.flagVal, c.flagChanged, c.targetScheme)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ResolveTransport(%q,%v,%q) = %q, nil; want error", c.flagVal, c.flagChanged, c.targetScheme, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTransport(%q,%v,%q) unexpected error: %v", c.flagVal, c.flagChanged, c.targetScheme, err)
			}
			if got != c.want {
				t.Errorf("ResolveTransport(%q,%v,%q) = %q; want %q", c.flagVal, c.flagChanged, c.targetScheme, got, c.want)
			}
		})
	}
}
