// Package visorconfig pkg/visor/visorconfig/types.go
//
// The Duration type's MarshalJSON / UnmarshalJSON live in
// types_native.go under //go:build !js because encoding/json
// drags the reflect runtime helpers TinyGo's stdlib lacks. Under
// js V1 is never (de)serialized, so the absence of those methods
// causes no functional change.
package visorconfig

import (
	"time"
)

// LogStore types.
const (
	FileLogStore   = "file"
	MemoryLogStore = "memory"
)

const (
	// DefaultTimeout is used for default config generation and if it is not set in config.
	DefaultTimeout = Duration(10 * time.Second)
	// DefaultLogRotationInterval is used as default for RotationInterval and if it is not set in Log.
	DefaultLogRotationInterval = Duration(time.Hour * 24 * 7)
)

// Duration wraps around time.Duration to allow parsing from and to JSON.
//
// The Marshal/Unmarshal methods live in types_native.go (!js) —
// browsers never (de)serialize V1, so dropping the methods under
// js doesn't change observable behavior; it just frees TinyGo
// from linking encoding/json's reflect path.
type Duration time.Duration
