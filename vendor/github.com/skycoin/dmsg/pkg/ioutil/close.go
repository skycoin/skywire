// Package ioutil pkg/ioutil/close.go
package ioutil

import (
	"io"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// CloseQuietly closes c, logging any error at debug level.
// Use this for deferred Close() calls where the error is not actionable.
func CloseQuietly(c io.Closer, log *logging.Logger) {
	if err := c.Close(); err != nil {
		if log != nil {
			log.WithError(err).Debug("Close error")
		}
	}
}
