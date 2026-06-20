package wasmhv

import (
	"bytes"
	"encoding/gob"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// This file adds the WRITE / control surface of the wasm hypervisor core:
// apps (list / start / stop / autostart) and transports (list / add / remove).
// As in core.go, it deliberately avoids importing pkg/visor (non-wasm) and
// instead declares MINIMAL MIRROR TYPES of the visor's RPC request/reply
// structs. gob matches struct fields BY NAME and ignores fields it can't map,
// so a partial mirror decodes the fields the UI reads and encodes the fields a
// control call needs.

// --- app mirror types (gob field names + json tags match pkg/visor/appserver) ---

// AppConfig mirrors the subset of appserver/spec.AppConfig the UI reads. It is
// embedded anonymously in AppState so both gob (field name "AppConfig") and
// json (promoted/flattened fields) line up with the real AppState.
type AppConfig struct {
	Name         string   `json:"name"`
	Binary       string   `json:"binary,omitempty"`
	Args         []string `json:"args,omitempty"`
	AutoStart    bool     `json:"auto_start"`
	Port         uint16   `json:"port"`
	LauncherMode string   `json:"launcher_mode,omitempty"`
}

// AppState mirrors appserver.AppState (AppConfig embedded + Status fields).
type AppState struct {
	AppConfig
	Status         int    `json:"status"`
	DetailedStatus string `json:"detailed_status"`
}

// startAppIn mirrors visor.StartAppIn (the StartApp RPC argument).
type startAppIn struct {
	AppName      string
	LauncherMode string
}

// setAutoStartIn mirrors visor.SetAutoStartIn (the SetAutoStart RPC argument).
type setAutoStartIn struct {
	AppName   string
	AutoStart bool
}

// --- transport mirror types ---

// TransportLogEntry mirrors transport.LogEntry on the wire. The real type uses
// a custom Gob/JSON codec (two uint64s; json {"recv":..,"sent":..}), so this
// mirror reproduces BOTH: GobDecode reads the two counters the server's
// GobEncode wrote, and MarshalJSON emits the {recv,sent} shape the UI reads.
type TransportLogEntry struct {
	RecvBytes uint64
	SentBytes uint64
}

// GobDecode mirrors transport.LogEntry.GobEncode: two separately-encoded uint64s.
func (le *TransportLogEntry) GobDecode(b []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(b))
	var rb, sb uint64
	if err := dec.Decode(&rb); err != nil {
		return err
	}
	if err := dec.Decode(&sb); err != nil {
		return err
	}
	atomic.StoreUint64(&le.RecvBytes, rb)
	atomic.StoreUint64(&le.SentBytes, sb)
	return nil
}

// MarshalJSON emits {"recv":N,"sent":N} to match transport.LogEntry.MarshalJSON.
func (le TransportLogEntry) MarshalJSON() ([]byte, error) {
	return []byte(`{"recv":` + strconv.FormatUint(le.RecvBytes, 10) +
		`,"sent":` + strconv.FormatUint(le.SentBytes, 10) + `}`), nil
}

// TransportSummary mirrors visor.TransportSummary.
type TransportSummary struct {
	ID        uuid.UUID          `json:"id"`
	Local     cipher.PubKey      `json:"local_pk"`
	Remote    cipher.PubKey      `json:"remote_pk"`
	Type      string             `json:"type"`
	Log       *TransportLogEntry `json:"log,omitempty"`
	IsSetup   bool               `json:"is_setup"`
	Label     string             `json:"label"`
	LatencyMS float64            `json:"latency_ms,omitempty"`
}

// transportsIn mirrors visor.TransportsIn (the Transports RPC argument).
type transportsIn struct {
	FilterTypes   []string
	FilterPubKeys []cipher.PubKey
	ShowLogs      bool
}

// addTransportIn mirrors visor.AddTransportIn (the AddTransport RPC argument).
type addTransportIn struct {
	RemotePK         cipher.PubKey
	TpType           string
	Timeout          time.Duration
	Label            string
	NoRegister       bool
	SkipLatencyProbe bool
}

// --- control calls ---

// apps fetches a visor's app list.
func (c *Core) apps(pk cipher.PubKey) ([]*AppState, error) {
	var reply []*AppState
	if err := c.call(pk, "Apps", &struct{}{}, &reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// app fetches a single app's state by name.
func (c *Core) app(pk cipher.PubKey, name string) (*AppState, error) {
	var reply AppState
	if err := c.call(pk, "App", &name, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// startApp starts an app (default launcher mode).
func (c *Core) startApp(pk cipher.PubKey, name string) error {
	return c.call(pk, "StartApp", &startAppIn{AppName: name}, &struct{}{})
}

// stopApp stops an app.
func (c *Core) stopApp(pk cipher.PubKey, name string) error {
	return c.call(pk, "StopApp", &name, &struct{}{})
}

// setAutoStart sets an app's autostart flag.
func (c *Core) setAutoStart(pk cipher.PubKey, name string, autostart bool) error {
	return c.call(pk, "SetAutoStart", &setAutoStartIn{AppName: name, AutoStart: autostart}, &struct{}{})
}

// transports fetches a visor's transport list (with logs).
func (c *Core) transports(pk cipher.PubKey) ([]*TransportSummary, error) {
	var reply []*TransportSummary
	if err := c.call(pk, "Transports", &transportsIn{ShowLogs: true}, &reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// transportTypes fetches the transport types a visor supports.
func (c *Core) transportTypes(pk cipher.PubKey) ([]string, error) {
	var reply []string
	if err := c.call(pk, "TransportTypes", &struct{}{}, &reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// addTransport creates a transport from the visor to remote of the given type.
func (c *Core) addTransport(pk cipher.PubKey, in addTransportIn) (*TransportSummary, error) {
	if in.TpType == "" {
		in.TpType = "skywire" // visor default when unspecified
	}
	if in.Timeout == 0 {
		in.Timeout = 30 * time.Second
	}
	var reply TransportSummary
	if err := c.call(pk, "AddTransport", &in, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// removeTransport deletes a transport by ID.
func (c *Core) removeTransport(pk cipher.PubKey, tid uuid.UUID) error {
	return c.call(pk, "RemoveTransport", &tid, &struct{}{})
}
