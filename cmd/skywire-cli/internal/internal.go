// Package internal contain functions used internally by different skywire-cli subcommands
package internal

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var log = logging.MustGetLogger("skywire-cli")

// Catch handles errors for skywire-cli commands packages
func Catch(err error, msgs ...string) {
	if err != nil {
		if len(msgs) > 0 {
			log.Fatalln(append(msgs, err.Error()))
		} else {
			log.Fatalln(err)
		}
	}
}

// ParsePK parses a public key
func ParsePK(name, v string) cipher.PubKey {
	var pk cipher.PubKey
	Catch(pk.Set(v), fmt.Sprintf("failed to parse <%s>:", name))
	return pk
}

// ParseUUID parses a uuid
func ParseUUID(name, v string) uuid.UUID {
	id, err := uuid.Parse(v)
	Catch(err, fmt.Sprintf("failed to parse <%s>:", name))
	return id
}
