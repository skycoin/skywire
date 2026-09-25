// Package visorapi pkg/visor/visorapi/pairing.go c3-vis-core
package visorapi

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/pairing"
)

// PairInfo is the public summary of a chat pair, returned by PairList
// and used as the poll cursor for PairPoll.
type PairInfo struct {
	PeerPK        cipher.PubKey  `json:"peer_pk"`
	Status        pairing.Status `json:"status"`
	Port          uint16         `json:"port"`
	EstablishedAt time.Time      `json:"established_at"`
	LastMessageAt time.Time      `json:"last_message_at,omitempty"`

	// Epoch is the short-lived key this conversation currently seals
	// under, as hex. Empty means the pair has not derived one yet and is
	// still on the legacy static key — which is worth showing, because
	// the two have materially different guarantees and nothing else in
	// the UI would distinguish them.
	Epoch string `json:"epoch,omitempty"`

	// ForwardSecret is true once an epoch exists, i.e. once messages
	// stop being openable with the two visors' identity keys alone.
	ForwardSecret bool `json:"forward_secret"`

	// KeyGeneration is how many ratchet keys this side has minted for
	// the pair. Each one retires the previous secret, so it doubles as
	// "how many times has the window closed behind us".
	KeyGeneration uint64 `json:"key_generation,omitempty"`
}

// PairMessage is one inbound message delivered through the visor's
// pair inbox. Outbound messages (sent via PairSend) are not echoed
// here; the caller already knows what it sent.
type PairMessage struct {
	PeerPK cipher.PubKey `json:"peer_pk"`
	Text   string        `json:"text"`
	TS     time.Time     `json:"ts"`

	// ID names this message on both sides of the pair. On a chat message
	// it is the message's own id (derived from the sender's timestamp);
	// on a delete record it is the id of the message being retracted.
	// Clients need it to correlate a delete with the bubble it removes.
	ID string `json:"id,omitempty"`

	// Type is empty for a chat message and pairing.MessageTypeDelete for
	// a retraction. A client that only knows about chat messages should
	// skip any record with a non-empty Type rather than render it.
	Type string `json:"type,omitempty"`
}

// PendingHypervisor is a peer that asked to drive this visor but is not yet
// in its hypervisor list.
type PendingHypervisor struct {
	PK          cipher.PubKey `json:"pk"`
	Fingerprint string        `json:"fingerprint"`
	FirstSeen   time.Time     `json:"first_seen"`
	LastSeen    time.Time     `json:"last_seen"`
	// Via is how the peer was noticed: "transport" (it holds a same-origin
	// transport to us) or "rpc" (it tried the transport RPC and was refused).
	Via string `json:"via"`
}

// PairCode is a one-time pairing code and when it stops working.
type PairCode struct {
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

// HypervisorFingerprint is the short, stable name a pending key is approved
// by: the first 40 bits of sha256(pk) as two hex groups ("a1b2c-3d4e5").
func HypervisorFingerprint(pk cipher.PubKey) string {
	sum := sha256.Sum256(pk[:])
	s := hex.EncodeToString(sum[:5])
	return s[:5] + "-" + s[5:]
}

// PairAddRequest is the input to RPC.PairAdd.
type PairAddRequest struct {
	PeerPK cipher.PubKey `json:"peer_pk"`
}

// PairSendRequest is the input to RPC.PairSend.
type PairSendRequest struct {
	PeerPK cipher.PubKey `json:"peer_pk"`
	Text   string        `json:"text"`
}

// PairDeleteRequest is the input to RPC.PairDelete.
type PairDeleteRequest struct {
	PeerPK cipher.PubKey `json:"peer_pk"`
	// ID is the message id returned by PairSend.
	ID string `json:"id"`
}

// PairPollRequest is the input to RPC.PairPoll.
type PairPollRequest struct {
	// Since is the lower bound (exclusive). Pass time.Time{} (zero)
	// to retrieve the entire current inbox window.
	Since time.Time `json:"since"`
}
