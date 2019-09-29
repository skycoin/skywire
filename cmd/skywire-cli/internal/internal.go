package internal

import (
	"fmt"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/google/uuid"
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

// Constants for default services.
const (
	DefaultTpDisc      = "http://transport.discovery.skywire.cc"
	DefaultDmsgDisc    = "http://dmsg.discovery.skywire.cc"
	DefaultRouteFinder = "http://routefinder.skywire.cc"
	DefaultSetupPK     = "026c5a07de617c5c488195b76e8671bf9e7ee654d0633933e202af9e111ffa358d"
)
