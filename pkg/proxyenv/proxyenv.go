// Package proxyenv pkg/proxyenv/proxyenv.go
//
// The proxy environment variables as curl reads them, for the command-line
// fetchers (the desk shell's curl, `skywire cli got`). A browser visor's desk
// shell exports ALL_PROXY=socks5h://127.0.0.1:4445, its resolving proxy, the
// way a shell profile beside a native visor would; a native shell sets whatever
// its user chose. Go's own http.ProxyFromEnvironment ignores ALL_PROXY, so a
// default here never reaches the visor's own HTTP clients.
package proxyenv

import (
	"net"
	"net/url"
	"strings"
)

// For returns the proxy the environment names for rawURL, or "" for none:
//
//   - http URLs read http_proxy, lowercase only, as curl does (HTTP_PROXY is
//     ignored there because a CGI program's environment can carry a request's
//     "Proxy:" header as HTTP_PROXY);
//   - https URLs read https_proxy, then HTTPS_PROXY;
//   - either falls back to ALL_PROXY, then all_proxy;
//   - no_proxy, then NO_PROXY, exempts hosts: a comma-separated list of names,
//     each matching itself and its subdomains, or "*" for every host.
func For(rawURL string, getenv func(string) string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	if exempt(u.Hostname(), first(getenv, "no_proxy", "NO_PROXY")) {
		return ""
	}
	var p string
	switch strings.ToLower(u.Scheme) {
	case "http":
		p = getenv("http_proxy")
	case "https":
		p = first(getenv, "https_proxy", "HTTPS_PROXY")
	}
	if p == "" {
		p = first(getenv, "ALL_PROXY", "all_proxy")
	}
	return strings.TrimSpace(p)
}

func first(getenv func(string) string, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// exempt reports whether noProxy covers host.
func exempt(host, noProxy string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, e := range strings.Split(noProxy, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if e == "*" {
			return true
		}
		if h, _, err := net.SplitHostPort(e); err == nil {
			e = h
		}
		e = strings.Trim(e, "[]")
		e = strings.TrimPrefix(e, ".")
		if host == e || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

// Names are the variables For reads, for a host that forwards them to the
// programs it runs.
var Names = []string{"http_proxy", "https_proxy", "HTTPS_PROXY", "ALL_PROXY", "all_proxy", "no_proxy", "NO_PROXY"}
