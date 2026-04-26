package svcmode

import (
	"context"
	"net/http"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"http", ModeHTTP, false},
		{"HTTP", ModeHTTP, false},
		{"  http  ", ModeHTTP, false},
		{"dmsg", ModeDmsg, false},
		{"dmsghttp", ModeDmsg, false},
		{"dual", ModeDual, false},
		{"both", ModeDual, false},
		{"", "", true},
		{"tcp", "", true},
		{"random", "", true},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestResolveMode(t *testing.T) {
	t.Setenv(ModeEnv, "")

	// Env override wins.
	t.Run("env override", func(t *testing.T) {
		t.Setenv(ModeEnv, "dmsg")
		got, err := ResolveMode("http", true)
		if err != nil {
			t.Fatal(err)
		}
		if got != ModeDmsg {
			t.Errorf("env override: got %v, want dmsg", got)
		}
	})

	// Flag value when env is empty.
	t.Run("flag value", func(t *testing.T) {
		t.Setenv(ModeEnv, "")
		got, err := ResolveMode("http", true)
		if err != nil {
			t.Fatal(err)
		}
		if got != ModeHTTP {
			t.Errorf("flag value: got %v, want http", got)
		}
	})

	// Default: SK present → dual.
	t.Run("default with SK", func(t *testing.T) {
		t.Setenv(ModeEnv, "")
		got, err := ResolveMode("", true)
		if err != nil {
			t.Fatal(err)
		}
		if got != ModeDual {
			t.Errorf("default with SK: got %v, want dual", got)
		}
	})

	// Default: no SK → http only.
	t.Run("default without SK", func(t *testing.T) {
		t.Setenv(ModeEnv, "")
		got, err := ResolveMode("", false)
		if err != nil {
			t.Fatal(err)
		}
		if got != ModeHTTP {
			t.Errorf("default without SK: got %v, want http", got)
		}
	})

	t.Run("bad env", func(t *testing.T) {
		t.Setenv(ModeEnv, "nonsense")
		if _, err := ResolveMode("", true); err == nil {
			t.Error("want error on bad env")
		}
	})
}

func TestModeIncludes(t *testing.T) {
	cases := []struct {
		mode     Mode
		wantHTTP bool
		wantDmsg bool
	}{
		{ModeHTTP, true, false},
		{ModeDmsg, false, true},
		{ModeDual, true, true},
	}
	for _, tc := range cases {
		if got := tc.mode.IncludesHTTP(); got != tc.wantHTTP {
			t.Errorf("%s.IncludesHTTP() = %v, want %v", tc.mode, got, tc.wantHTTP)
		}
		if got := tc.mode.IncludesDmsg(); got != tc.wantDmsg {
			t.Errorf("%s.IncludesDmsg() = %v, want %v", tc.mode, got, tc.wantDmsg)
		}
	}
}

func TestStartValidation(t *testing.T) {
	// Generate a real keypair for the dmsg-mode cases.
	pk, sk := cipher.GenerateKeyPair()
	handler := http.NewServeMux()

	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "missing handler",
			cfg:     Config{Mode: ModeHTTP, HTTPAddr: ":0"},
			wantErr: "Handler",
		},
		{
			name:    "missing mode",
			cfg:     Config{Handler: handler, HTTPAddr: ":0"},
			wantErr: "Mode",
		},
		{
			name:    "http mode without addr",
			cfg:     Config{Mode: ModeHTTP, Handler: handler},
			wantErr: "HTTPAddr",
		},
		{
			name:    "dmsg mode without SK",
			cfg:     Config{Mode: ModeDmsg, Handler: handler, PK: pk, DmsgDiscovery: "http://example"},
			wantErr: "secret key",
		},
		{
			name: "dmsg bootstrap without source",
			cfg: Config{
				Mode: ModeDmsg, Handler: handler, PK: pk, SK: sk,
			},
			wantErr: "EmbeddedDmsgServers",
		},
		{
			// http mode + SK should still attempt dmsg bootstrap
			// (Handle.DmsgClient should be set) and therefore
			// needs a bootstrap source. This verifies the new
			// "http mode doesn't prevent dmsg client" semantics.
			name: "http mode with SK but no bootstrap source",
			cfg: Config{
				Mode: ModeHTTP, HTTPAddr: ":0", Handler: handler, PK: pk, SK: sk,
			},
			wantErr: "EmbeddedDmsgServers",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Start(ctx, tc.cfg)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !containsI(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// containsI is a case-insensitive substring check without importing strings.
func containsI(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a, b := s[i+j], substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
