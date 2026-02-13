// Package skynet internal/skynet/common.go
package skynet

import (
	"net"
	"time"
)

func timeoutAfter(seconds int) time.Time {
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}
