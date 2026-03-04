// Package skysocksc root.go
package skysocksc

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// isTestEnv checks if test environment is enabled via SKYWIRETEST env var
func isTestEnv() bool {
	return os.Getenv("SKYWIRETEST") == "1"
}

// cacheDirPath returns a cache directory path based on the service URL host
func cacheDirPath(serviceURL string) string {
	u, err := url.Parse(serviceURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), u.Host)
}

// cacheFile returns the full cache file path for a given URL.
// If cacheDir is empty, returns "" (disables caching).
// Creates the cache directory if it doesn't exist.
// Generates simple, descriptive filenames (e.g., "proxy.json", "uptimes.json").
func cacheFile(cacheDir, fullURL string) string {
	if cacheDir == "" {
		return ""
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return ""
	}

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return ""
	}

	// Extract a simple, meaningful name from the URL
	var name string

	// Check for service type in query (e.g., ?type=proxy -> proxy.json)
	if typeVal := u.Query().Get("type"); typeVal != "" {
		name = typeVal
	} else {
		// Use the last path segment (e.g., /uptimes -> uptimes.json)
		path := strings.TrimSuffix(u.Path, "/")
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			name = path[idx+1:]
		} else {
			name = strings.TrimPrefix(path, "/")
		}
	}

	if name == "" {
		name = "cache"
	}

	return filepath.Join(cacheDir, name+".json")
}

// getDeployment returns the appropriate deployment config based on test env
func getDeployment() deployment.Services {
	if isTestEnv() {
		return deployment.Test
	}
	return deployment.Prod
}

var (
	binaryName      = "skysocks-client"
	stateName       = "skysocks-client"
	serviceType     = servicedisc.ServiceTypeProxy
	rawData         bool
	sdURL           string
	utURL           string
	cacheDirSD      string
	cacheDirUT      string
	cacheFilesAge   int
	isStats         bool
	pubkey          cipher.PubKey
	pk              string
	allClients      bool
	noFilterOnline  bool
	clientName      string
	country         string
	version         string
	minVersion      string
	maxVersion      string
	showOffline     bool
	addr            string
	startingTimeout int
	httpAddr        string
	jsonOutput      bool
	appPort         uint16
	useInternal     bool
	useExternal     bool
	// test command vars
	testURL         string
	testTimeout     int
	testBatchSize   int
	testDelay       int
	testOnlyWithTp  bool
	testVerbose     bool
	testConnectOnly bool
	testVersion     string
	// existing transport flag
	existingTpOnly   bool
	forceLocalRoutes bool
	// multi-hop testing
	viaVisor string
	testEnv  bool
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
