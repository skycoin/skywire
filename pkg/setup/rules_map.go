package setup

import (
	"encoding/json"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/routing"
)

// RulesMap associates a slice of rules to a visor's public key.
type RulesMap map[cipher.PubKey][]routing.Rule

// String implements fmt.Stringer
func (rm RulesMap) String() string {
	out := make(map[cipher.PubKey][]string, len(rm))

	for pk, rules := range rm {
		str := make([]string, len(rules))
		for i, rule := range rules {
			str[i] = rule.String()
		}

		out[pk] = str
	}

	jb, err := json.MarshalIndent(out, "", "\t")
	if err != nil {
		panic(err)
	}

	return string(jb)
}
