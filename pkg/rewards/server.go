package rewards

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// WhitelistAuth returns a gin middleware that checks if the remote PK
// (from DMSG/skynet RemoteAddr) is in the whitelist. Exported so the
// visor can use the same auth pattern.
func WhitelistAuth(wlkeys []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(wlkeys) == 0 {
			c.Next()
			return
		}
		remotePK := extractPK(c)
		if remotePK.Null() {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		for _, pk := range wlkeys {
			if pk == remotePK {
				c.Next()
				return
			}
		}
		c.AbortWithStatus(http.StatusUnauthorized)
	}
}

// IsWhitelisted checks if the current request is from a whitelisted PK
// without blocking. Used for UI rendering (show/hide links).
func IsWhitelisted(c *gin.Context, wlkeys []cipher.PubKey) bool {
	if len(wlkeys) == 0 {
		return true
	}
	remotePK := extractPK(c)
	if remotePK.Null() {
		return false
	}
	for _, pk := range wlkeys {
		if pk == remotePK {
			return true
		}
	}
	return false
}

func extractPK(c *gin.Context) cipher.PubKey {
	host := c.Request.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return cipher.PubKey{}
	}
	return pk
}
