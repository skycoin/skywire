package got

import (
	"mime"
	"net/url"
	"path/filepath"
	"strings"
)

// DefaultFileName is the fallback name when a filename cannot be derived from the URL.
var DefaultFileName = "got.output"

// GetFilename returns a filename derived from a URL path, or DefaultFileName.
func GetFilename(URL string) string {
	if u, err := url.Parse(URL); err == nil && filepath.Ext(u.Path) != "" {
		return filepath.Base(u.Path)
	}
	return DefaultFileName
}

// getNameFromHeader extracts a filename from a Content-Disposition header value.
// Returns empty string if the header is missing, malformed, or contains path traversal.
func getNameFromHeader(val string) string {
	_, params, err := mime.ParseMediaType(val)
	if err != nil {
		return ""
	}
	name := params["filename"]
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return ""
	}
	return name
}
