package rewards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

// XpubIndexFile sits in the reward system's working directory, outside the served hist dir.
const XpubIndexFile = "xpub_index.json"

// maxXpubScan bounds how many used addresses are skipped when looking for an unused one.
const maxXpubScan = 1000

// XpubIndex records the external chain index paid to each xpub for each reward date,
// so no address is paid twice. Keys are SHA-256 hashes, so the xpubs are not stored.
type XpubIndex struct {
	path string
	m    map[string]map[string]uint32
}

func xpubKey(xpub string) string {
	h := sha256.Sum256([]byte(xpub))
	return hex.EncodeToString(h[:])
}

// LoadXpubIndex reads the record at path. A missing file is an empty record.
func LoadXpubIndex(path string) (*XpubIndex, error) {
	x := &XpubIndex{path: path, m: map[string]map[string]uint32{}}
	b, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return x, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &x.m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return x, nil
}

// MaxIndex is the highest index assigned to xpub, and false if it was never assigned one.
func (x *XpubIndex) MaxIndex(xpub string) (uint32, bool) {
	var maxIdx uint32
	found := false
	for _, i := range x.m[xpubKey(xpub)] {
		if !found || i > maxIdx {
			maxIdx, found = i, true
		}
	}
	return maxIdx, found
}

// Assign returns the index to pay xpub at for date. A date keeps the index it was
// first given. A new one is past every assigned index and every address used reports as seen.
func (x *XpubIndex) Assign(xpub, date string, used func(addr string) (bool, error)) (uint32, error) {
	k := xpubKey(xpub)
	if i, ok := x.m[k][date]; ok {
		return i, nil
	}
	var start uint32
	if maxIdx, ok := x.MaxIndex(xpub); ok {
		start = maxIdx + 1
	}
	for i := start; i < start+maxXpubScan; i++ {
		addr, err := rewardconfig.DeriveExternalAddressFromXpub(xpub, i)
		if err != nil {
			return 0, err
		}
		u, err := used(addr)
		if err != nil {
			return 0, err
		}
		if u {
			continue
		}
		if x.m[k] == nil {
			x.m[k] = map[string]uint32{}
		}
		x.m[k][date] = i
		return i, x.save()
	}
	return 0, fmt.Errorf("no unused address in %d indexes from %d", maxXpubScan, start)
}

func (x *XpubIndex) save() error {
	b, err := json.MarshalIndent(x.m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(x.path), ".xpub_index-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if _, err := tmp.Write(b); err != nil {
		tmp.Close() //nolint:errcheck,gosec
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), x.path)
}

// NodeAddressUsed reports whether an address has ever received coins, using a skycoin node API.
func NodeAddressUsed(nodeURL string) func(addr string) (bool, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	return func(addr string) (bool, error) {
		resp, err := c.Get(strings.TrimRight(nodeURL, "/") + "/api/v1/address_uxouts?address=" + url.QueryEscape(addr))
		if err != nil {
			return false, err
		}
		defer resp.Body.Close() //nolint:errcheck
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if err != nil {
			return false, err
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("skycoin node %s returned %d: %s", nodeURL, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var uxouts []json.RawMessage
		if err := json.Unmarshal(body, &uxouts); err != nil {
			return false, fmt.Errorf("decode address_uxouts: %w", err)
		}
		return len(uxouts) > 0, nil
	}
}
