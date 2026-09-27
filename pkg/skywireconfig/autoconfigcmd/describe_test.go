// Package autoconfigcmd pkg/skywireconfig/autoconfigcmd/describe_test.go c3-vis-core
package autoconfigcmd

import "testing"

func TestEditFor(t *testing.T) {
	cases := []struct {
		flag, in, key, want string
	}{
		{"ishv", "true", "ISHYPERVISOR", "true"},
		{"no-ishv", "true", "ISHYPERVISOR", "false"},
		{"minsess", "3", "MINDMSGSESS", "3"},
		{"hvpks", "a,b", "HYPERVISORPKS", "('a' 'b')"},
		{"hvdeskaddr", ":8010", "HVDESKADDR", "':8010'"},
	}
	for _, c := range cases {
		e, err := EditFor(c.flag, c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.flag, err)
		}
		if e.Key != c.key || e.Value != c.want {
			t.Errorf("%s=%s: got %s=%s, want %s=%s", c.flag, c.in, e.Key, e.Value, c.key, c.want)
		}
	}
	for _, bad := range [][2]string{{"verbose", "true"}, {"ishv", "maybe"}, {"minsess", "x"}} {
		if _, err := EditFor(bad[0], bad[1]); err == nil {
			t.Errorf("%s=%s: want an error", bad[0], bad[1])
		}
	}
}

func TestDescribeCarriesEnvMapping(t *testing.T) {
	var ishv, verbose *Flag
	flags := Describe()
	for i := range flags {
		switch flags[i].Name {
		case "ishv":
			ishv = &flags[i]
		case "verbose":
			verbose = &flags[i]
		}
	}
	if ishv == nil || ishv.EnvKey != "ISHYPERVISOR" || ishv.EnvFormat != "bool" {
		t.Fatalf("ishv: %+v", ishv)
	}
	if verbose == nil || verbose.EnvKey != "" {
		t.Fatalf("verbose should write nothing: %+v", verbose)
	}
}
