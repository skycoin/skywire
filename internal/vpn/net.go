package vpn

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
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
	dataBytes, err := ioutil.ReadAll(conn)
	if err != nil {
		return fmt.Errorf("error reading data: %w", err)
	}

	if err := json.Unmarshal(dataBytes, data); err != nil {
		return fmt.Errorf("error unmarshaling data: %w", err)
	}

	return nil
}
