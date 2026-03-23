// Package vpn internal/vpn/net.go
package vpn

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// WriteJSONWithTimeout marshals `data` and sends it over the `conn` with the specified write `timeout`.
func WriteJSONWithTimeout(conn net.Conn, data interface{}, timeout time.Duration) error {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	if err := WriteJSON(conn, data); err != nil {
		return err
	}

	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		return fmt.Errorf("failed to remove write deadline: %w", err)
	}

	return nil
}

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

// ReadJSONWithTimeout reads portion of data from the `conn` and unmarshals it into `data` with the
// specified read `timeout`.
func ReadJSONWithTimeout(conn net.Conn, data interface{}, timeout time.Duration) error {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("failed to set read deadline: %w", err)
	}

	if err := ReadJSON(conn, data); err != nil {
		return err
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("failed to remove read deadline: %w", err)
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
			fmt.Printf("error reading data: %v\n", err)
			return err
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
