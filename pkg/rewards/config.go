// Package rewards provides the reward system UI as a mountable HTTP
// handler. It can be used standalone (via the CLI) or embedded in the
// visor's port 80 gin router for integrated DMSG/skynet access.
package rewards

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// Config holds all the configuration needed to run the reward system UI.
type Config struct {
	// StartTime is when the server started (for /health uptime).
	StartTime time.Time

	// PK and SK for DMSG client (only needed for standalone mode).
	PK cipher.PubKey
	SK cipher.SecKey

	// DmsgDisc is the DMSG discovery URL (standalone mode).
	DmsgDisc string
	// DmsgPort is the DMSG port to serve on (standalone mode).
	DmsgPort uint16
	// DmsgSessions is the number of DMSG sessions (standalone mode).
	DmsgSessions int

	// WebPort is the HTTP port (standalone mode).
	WebPort uint

	// WhitelistPKs is the list of PKs allowed to POST reward
	// transactions and access protected endpoints.
	WhitelistPKs []cipher.PubKey

	// WorkDir is the directory containing log_collection and reward
	// hist directories.
	WorkDir string

	// CanonicalDomain is the public domain name for SEO/sitemap
	// (e.g., "theskywirenetwork.net").
	CanonicalDomain string

	// HealthOnly disables all endpoints except /health.
	HealthOnly bool

	// EnsureOnlineURL is the URL to check for internet connectivity.
	EnsureOnlineURL string

	// LoginNode is the address of the login chain node.
	LoginNode string
	// SkycoinNode is the address of the skycoin node for tx broadcast.
	SkycoinNode string
	// LoginChainFlags is additional flags for login chain.
	LoginChainFlags string

	// Logger for the reward system.
	Log *logging.Logger
}
