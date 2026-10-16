package visor

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// lotHandler serves LinuxOnTab at /lot/ from a directory, or proxies it from a
// site that publishes it, such as https://next.linuxontab.com.
func lotHandler(src string) (http.Handler, error) {
	if !strings.HasPrefix(src, "https://") && !strings.HasPrefix(src, "http://") {
		return http.StripPrefix("/lot/", http.FileServer(http.Dir(src))), nil
	}
	up, err := url.Parse(strings.TrimRight(src, "/"))
	if err != nil || up.Host == "" {
		return nil, fmt.Errorf("--lot %q: not a directory or a site URL", src)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(up)
			pr.Out.Header.Del("Cookie")
		},
		// The frame must stay on the desk's origin and under the desk's own
		// isolation headers, so the site's are dropped and its redirects kept
		// under /lot/.
		ModifyResponse: func(resp *http.Response) error {
			for _, h := range []string{"Cross-Origin-Opener-Policy", "Cross-Origin-Embedder-Policy", "Cross-Origin-Resource-Policy", "Set-Cookie"} {
				resp.Header.Del(h)
			}
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", lotLocation(loc, up))
			}
			return nil
		},
	}
	return http.StripPrefix("/lot", rp), nil
}

// lotLocation maps a redirect from the LinuxOnTab site to the same place
// under /lot/. A redirect to another site is left as it is.
func lotLocation(loc string, up *url.URL) string {
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}
	if u.Host != "" && u.Host != up.Host {
		return loc
	}
	if u.Host == "" && !strings.HasPrefix(u.Path, "/") {
		return loc
	}
	p := strings.TrimPrefix(u.Path, up.Path)
	out := url.URL{Path: "/lot" + p, RawQuery: u.RawQuery, Fragment: u.Fragment}
	return out.String()
}
