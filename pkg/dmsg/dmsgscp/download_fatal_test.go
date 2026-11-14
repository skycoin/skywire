// Package dmsgscp pkg/dmsg/dmsgscp/download_fatal_test.go c3-net-dmsg
package dmsgscp

import (
	"bufio"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A host that cannot serve the path sends a fatal line, and Download
// returns its message rather than a header parse error.
func TestDownloadReturnsHostFatal(t *testing.T) {
	cConn, hConn := net.Pipe()
	defer cConn.Close() //nolint:errcheck
	defer hConn.Close() //nolint:errcheck

	go func() {
		if _, err := bufio.NewReader(hConn).ReadString('\n'); err != nil {
			return
		}
		_ = WriteFatal(hConn, "open /missing: no such file or directory") //nolint:errcheck
	}()

	c := NewClient(cConn, cipher.PubKey{})
	err := c.Download("/missing", filepath.Join(t.TempDir(), "out"))
	var fe *FatalError
	require.True(t, errors.As(err, &fe), "got %v", err)
	require.Equal(t, "open /missing: no such file or directory", fe.Msg)
}
