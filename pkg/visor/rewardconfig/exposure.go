// Package rewardconfig pkg/visor/rewardconfig/exposure.go c3-vis-core
package rewardconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	return "/api/v1/address_uxouts?address=" + url.QueryEscape(addr)
}

// FetchAddressExposure asks the skycoin node at nodeURL whether addr has ever spent.
func FetchAddressExposure(c *http.Client, nodeURL, addr string) (AddressExposure, error) {
	resp, err := c.Get(strings.TrimRight(nodeURL, "/") + AddressUxoutsPath(addr))
	if err != nil {
		return AddressExposure{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return AddressExposure{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return AddressExposure{}, fmt.Errorf("skycoin node %s returned %d: %s", nodeURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return ParseAddressUxouts(addr, body)
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

// Warning is the text shown for an address that has sent coins, or "" when it never spent.
func (e AddressExposure) Warning() string {
	if !e.Spent {
		return ""
	}
	w := "WARNING: reward address " + e.Address + " has sent coins, so it no longer earns rewards.\n" +
		"Set your wallet's account xpub as the reward address: skywire cli reward XPUB\n" +
		"Its public key is also on the blockchain, where a future quantum computer could use it to take coins sent there.\n"
	if e.Balance != "0" {
		w += "It holds " + e.Balance + " SKY now. Move them to an address of the xpub's wallet.\n"
	}
	return strings.TrimSuffix(w, "\n")
}
