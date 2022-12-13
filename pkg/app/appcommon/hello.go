// Package appcommon pkg/app/appcommon/hello.go
package appcommon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Hello represents the first JSON object that an app sends the visor.
type Hello struct {
	ProcKey    ProcKey         `json:"proc_key"`              // proc key
	EgressNet  string          `json:"egress_net,omitempty"`  // network which hosts the appevent.RPCGateway of the app
	EgressAddr string          `json:"egress_addr,omitempty"` // address which hosts the appevent.RPCGateway of the app
	EventSubs  map[string]bool `json:"event_subs,omitempty"`  // event subscriptions
}

// String implements fmt.Stringer
func (h *Hello) String() string {
	j, err := json.Marshal(h)
	if err != nil {
		panic(err) // should never happen
	}
	return string(j)
}

// AllowsEventType returns true if the hello object contents allow for an event type.
func (h *Hello) AllowsEventType(eventType string) bool {
	if h.EventSubs == nil {
		return false
	}
	return h.EventSubs[eventType]
}

// ReadHello reads in a hello object from the given reader.
func ReadHello(r io.Reader) (Hello, error) {
	sizeRaw := make([]byte, 2)
	if _, err := io.ReadFull(r, sizeRaw); err != nil {
		return Hello{}, fmt.Errorf("failed to read hello size prefix: %w", err)
	}
	size := binary.BigEndian.Uint16(sizeRaw)

	helloRaw := make([]byte, size)
	if _, err := io.ReadFull(r, helloRaw); err != nil {
		return Hello{}, fmt.Errorf("failed to read hello data: %w", err)
	}

	var hello Hello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		return Hello{}, fmt.Errorf("failed to unmarshal hello data: %w", err)
	}

	return hello, nil
}

// WriteHello writes a hello object into a given writer.
func WriteHello(w io.Writer, hello Hello) error {
	helloRaw, err := json.Marshal(hello)
	if err != nil {
		panic(err) // should never happen
	}

	raw := make([]byte, 2+len(helloRaw))
	size := len(helloRaw)
	binary.BigEndian.PutUint16(raw[:2], uint16(size))
	if n := copy(raw[2:], helloRaw); n != size {
		panic("hello write does not add up")
	}

	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("failed to write hello data: %w", err)
	}
	return nil
}
