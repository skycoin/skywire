//go:build !mobile

// Package visor pkg/visor/api_browse_jar.go c3-vis-browse
package visor

import (
	"net/http"
	"net/http/cookiejar"
	"sync"

	"golang.org/x/net/publicsuffix"
)

var browseCookies struct {
	mu  sync.Mutex
	jar http.CookieJar
}

// browseJar returns the cookie jar the browse fetches share.
func browseJar() http.CookieJar {
	browseCookies.mu.Lock()
	defer browseCookies.mu.Unlock()
	if browseCookies.jar == nil {
		jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		if err != nil {
			return nil
		}
		browseCookies.jar = jar
	}
	return browseCookies.jar
}
