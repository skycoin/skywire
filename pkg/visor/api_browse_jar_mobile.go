//go:build mobile

// Package visor pkg/visor/api_browse_jar_mobile.go c3-vis-browse
package visor

import "net/http"

// browseJar has no jar to give: browse is not part of the mobile build, and the
// public suffix list a jar needs would embed binary tables F-Droid rejects.
func browseJar() http.CookieJar { return nil }
