// Package setup pkg/setup/rules_map.go
package setup

import (
	"encoding/json"

	"github.com/skycoin/skywire/pkg/routing"
)

// RulesMap associates a slice of rules to a visor's string of `public key:port“.
type RulesMap map[string][]routing.Rule

// String implements fmt.Stringer
func (rm RulesMap) String() string {
	out := make(map[string][]string, len(rm))

	for addr, rules := range rm {
		str := make([]string, len(rules))
		for i, rule := range rules {
			str[i] = rule.String()
		}

		out[addr] = str
	}

	jb, err := json.MarshalIndent(out, "", "\t")
	if err != nil {
		panic(err)
	}

	return string(jb)
}
