package appcommon

import (
	"github.com/google/uuid"
)

// Key is an app key to authenticate within the
// app server.
type Key string

// GenerateAppKey generates new app key.
func GenerateAppKey() Key {
	return Key(uuid.New().String())
}
