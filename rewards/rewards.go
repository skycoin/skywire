// Package rewards github.com/skycoin/skywire/rewards/rewards.go
//
//go:generate go run ../cmd/gen/gen.go -jo arches.json
package rewards

import (
	_ "embed"
	"encoding/json"
	"log"
)

// ArchesJSON is the embedded arches.json file
// go run ../cmd/gen/gen.go -jo arches.json
// ["amd64","arm64","386","arm","ppc64","riscv64","wasm","loong64","mips","mips64","mips64le","mipsle","ppc64le","s390x"]
//
//go:embed arches.json
var ArchesJSON []byte

// Architectures is an array of GOARCH architectures
var Architectures []string

// MainnetRules is the mainnet_rules.md
// print the mainnet rules with `skywire cli reward rules`
//
//go:embed mainnet_rules.md
var MainnetRules string

func init() {
	err := json.Unmarshal(ArchesJSON, &Architectures)
	if err != nil {
		log.Panic("arches.json ", err)
	}
}
