package noisewrapper

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/SkycoinProject/dmsg/noise"
)

// HSTimeout sets handshake timeout.
const HSTimeout = 5 * time.Second

// WrapConn wraps `conn` with noise.
func WrapConn(config noise.Config, conn net.Conn) (net.Conn, error) {
	ns, err := noise.New(noise.HandshakeKK, config)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare stream noise object: %w", err)
	}

	fmt.Fprintf(os.Stdout, "TEST: PREPARED NOISE OBJECT\n")

	wrappedConn, err := noise.WrapConn(conn, ns, HSTimeout)
	if err != nil {
		return nil, fmt.Errorf("error performing noise handshake: %w", err)
	}

	return wrappedConn, nil
}
