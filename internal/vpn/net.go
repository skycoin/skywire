package vpn

import (
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/noise"

	"github.com/skycoin/skywire/pkg/app/appnet"
)

// WriteJSON marshals `data` and sends it over the `conn`.
func WriteJSON(conn net.Conn, data interface{}) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("error marshaling data: %w", err)
	}

	for n, totalSent := 0, 0; totalSent < len(dataBytes); totalSent += n {
		n, err = conn.Write(dataBytes[totalSent:])
		if err != nil {
			return fmt.Errorf("error sending data: %w", err)
		}

		totalSent += n
	}

	return nil
}

// ReadJSON reads portion of data from the `conn` and unmarshals it into `data`.
func ReadJSON(conn net.Conn, data interface{}) error {
	const bufSize = 1024

	var dataBytes []byte
	buf := make([]byte, bufSize)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return fmt.Errorf("error reading data: %w", err)
		}

		dataBytes = append(dataBytes, buf[:n]...)

		if n < 1024 {
			break
		}
	}

	if err := json.Unmarshal(dataBytes, data); err != nil {
		return fmt.Errorf("error unmarshaling data: %w", err)
	}

	return nil
}

// WrapRWWithNoise wraps `conn` with noise.
func WrapRWWithNoise(conn net.Conn, initiator bool, pk cipher.PubKey, sk cipher.SecKey) (io.ReadWriter, error) {
	remoteAddr, isAppConn := conn.RemoteAddr().(appnet.Addr)
	if isAppConn {
		ns, err := noise.New(noise.HandshakeKK, noise.Config{
			LocalPK:   pk,
			LocalSK:   sk,
			RemotePK:  remoteAddr.PubKey,
			Initiator: initiator,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prepare stream noise object: %w", err)
		}

		rw := noise.NewReadWriter(conn, ns)
		if err := rw.Handshake(HSTimeout); err != nil {
			return nil, fmt.Errorf("error performing noise handshake: %w", err)
		}

		return rw, nil
	}

	// shouldn't happen, but no encryption in this case
	return conn, nil
}
