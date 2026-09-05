package main

import "testing"

// TestVnetTarget pins the spellings that name the visor's own loopback, and
// the ones that must NOT be claimed. A claimed page is rendered UNSANDBOXED
// out of this origin, so a false positive here would hand an arbitrary URL
// same-origin privileges.
func TestVnetTarget(t *testing.T) {
	claimed := map[string][2]string{
		"http://vnet:8002/":                   {"8002", "/"},
		"http://vnet:8001/?embed=1#/?embed=1": {"8001", "/?embed=1#/?embed=1"},
		"http://8002.vnet/":                   {"8002", "/"},
		"http://8002.vnet/cli/dmsg":           {"8002", "/cli/dmsg"},
		"http://localhost:8002/prose/":        {"8002", "/prose/"},
		"http://127.0.0.1:8002/":              {"8002", "/"},
		"http://VNET:8002/Mixed":              {"8002", "/Mixed"},
	}
	for in, want := range claimed {
		port, path := vnetTarget(in)
		if port != want[0] || path != want[1] {
			t.Errorf("vnetTarget(%q) = (%q,%q), want (%q,%q)", in, port, path, want[0], want[1])
		}
	}

	for _, in := range []string{
		"http://example.com/",         // someone else entirely
		"http://notvnet/",             // no port, not <n>.vnet
		"http://vnet/",                // bare vnet, no port
		"http://abc.vnet/",            // non-numeric label
		"http://evil.com:8002/",       // right port, wrong host
		"http://vnet.example.com:80/", // vnet as a subdomain label
		"",                            // nothing
		"::::",                        // unparseable
	} {
		if port, _ := vnetTarget(in); port != "" {
			t.Errorf("vnetTarget(%q) claimed port %q — must not be claimed", in, port)
		}
	}
}
