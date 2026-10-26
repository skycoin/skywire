package rewardconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skycoin/skycoin/src/util/droplet"
)

// AddressExposure says whether an address has ever spent. A spend puts the
// address's public key on chain, which a quantum computer could use to take what it holds.
type AddressExposure struct {
	Address string `json:"address"`
	Spent   bool   `json:"spent"`
	Balance string `json:"balance"`
}

// AddressUxoutsPath is the skycoin node API query that ParseAddressUxouts reads.
func AddressUxoutsPath(addr string) string {
	return "/api/v1/address_uxouts?address=" + addr
}

// ParseAddressUxouts reads a node's address_uxouts response for addr.
func ParseAddressUxouts(addr string, body []byte) (AddressExposure, error) {
	var uxouts []struct {
		Coins         uint64 `json:"coins"`
		SpentBlockSeq uint64 `json:"spent_block_seq"`
	}
	if err := json.Unmarshal(body, &uxouts); err != nil {
		return AddressExposure{}, fmt.Errorf("decode address_uxouts: %w", err)
	}
	e := AddressExposure{Address: addr}
	var bal uint64
	for _, ux := range uxouts {
		if ux.SpentBlockSeq > 0 {
			e.Spent = true
		} else {
			bal += ux.Coins
		}
	}
	s, err := droplet.ToString(bal)
	if err != nil {
		return AddressExposure{}, err
	}
	e.Balance = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	return e, nil
}

// Warning is the text shown for an exposed address, or "" when it never spent.
func (e AddressExposure) Warning() string {
	if !e.Spent {
		return ""
	}
	w := "WARNING: reward address " + e.Address + " has sent coins before, so its public key\n" +
		"is on the blockchain. A future quantum computer could use it to steal coins sent there.\n"
	if e.Balance != "0" {
		w += "It holds " + e.Balance + " SKY now. Move them to a new address.\n"
	}
	return w + "Set a new address or a BIP44 account xpub as your reward address."
}
