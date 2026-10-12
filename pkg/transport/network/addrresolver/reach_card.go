package addrresolver

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ReachPath is where a visor serves its reach card, over dmsg and skynet.
const ReachPath = "/reach"

// ErrReachCardSig is returned when a reach card is not signed by its owner.
var ErrReachCardSig = errors.New("reach card: signature does not match its owner")

// ReachCard is a visor's own answer to "how do I dial you", signed by it: the
// records the address resolver would return for it, one per transport type.
// Peers ask the visor for it directly, so the resolver is needed only to tell
// the visor what to put in it.
type ReachCard struct {
	PK cipher.PubKey `json:"pk"`
	At int64         `json:"at"` // unix seconds when it was built
	// Records is the signed JSON of a map from transport type to VisorData,
	// kept as sent so a field added later does not break the signature.
	Records json.RawMessage `json:"records"`
	Sig     cipher.Sig      `json:"sig"`
}

// NewReachCard builds and signs pk's card from records keyed by type.
func NewReachCard(pk cipher.PubKey, sk cipher.SecKey, at time.Time, records map[types.Type]VisorData) (*ReachCard, error) {
	norm := make(map[types.Type]VisorData, len(records))
	for t, d := range records {
		norm[types.NormalizeType(t)] = d
	}
	raw, err := json.Marshal(norm)
	if err != nil {
		return nil, err
	}
	c := &ReachCard{PK: pk, At: at.Unix(), Records: raw}
	if c.Sig, err = cipher.SignPayload(c.payload(), sk); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *ReachCard) payload() []byte {
	buf := make([]byte, 0, len(c.PK)+8+len(c.Records))
	buf = append(buf, c.PK[:]...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(c.At)) //nolint:gosec // unix seconds
	return append(buf, c.Records...)
}

// Verify checks the card is pk's and signed by it, and returns its records.
func (c *ReachCard) Verify(pk cipher.PubKey) (map[types.Type]VisorData, error) {
	if c.PK != pk {
		return nil, ErrReachCardSig
	}
	if err := cipher.VerifyPubKeySignedPayload(c.PK, c.Sig, c.payload()); err != nil {
		return nil, ErrReachCardSig
	}
	var recs map[types.Type]VisorData
	if err := json.Unmarshal(c.Records, &recs); err != nil {
		return nil, err
	}
	out := make(map[types.Type]VisorData, len(recs))
	for t, d := range recs {
		out[types.NormalizeType(t)] = d
	}
	return out, nil
}
