package disc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/skycoin/dmsg/cipher"
)

const (
	currentVersion             = "0.0.1"
	entryLifetime              = 1 * time.Minute
	allowedEntryTimestampError = 100 * time.Millisecond
)

var (
	// ErrKeyNotFound occurs in case when entry of public key is not found
	ErrKeyNotFound = errors.New("entry of public key is not found")
	// ErrNoAvailableServers occurs when dmsg client cannot find any delegated servers available for the given remote.
	ErrNoAvailableServers = errors.New("no delegated dmsg servers available for remote")
	// ErrUnexpected occurs in case when something unexpected happened
	ErrUnexpected = errors.New("something unexpected happened")
	// ErrUnauthorized occurs in case of invalid signature
	ErrUnauthorized = errors.New("invalid signature")
	// ErrBadInput occurs in case of bad input
	ErrBadInput = errors.New("error bad input")
	// ErrValidationNonZeroSequence occurs in case when new entry has non-zero sequence
	ErrValidationNonZeroSequence = NewEntryValidationError("new entry has non-zero sequence")
	// ErrValidationNilEphemerals occurs in case when entry of client instance has nil ephemeral keys
	ErrValidationNilEphemerals = NewEntryValidationError("entry of client instance has nil ephemeral keys")
	// ErrValidationNilKeys occurs in case when entry Keys is nil
	ErrValidationNilKeys = NewEntryValidationError("entry Keys is nil")
	// ErrValidationNonNilEphemerals occurs in case when entry of server instance has non nil Keys.Ephemerals field
	ErrValidationNonNilEphemerals = NewEntryValidationError("entry of server instance has non nil Keys.Ephemerals field")
	// ErrValidationNoSignature occurs in case when entry has no signature
	ErrValidationNoSignature = NewEntryValidationError("entry has no signature")
	// ErrValidationNoVersion occurs in case when entry has no version
	ErrValidationNoVersion = NewEntryValidationError("entry has no version")
	// ErrValidationNoClientOrServer occurs in case when entry has neither client or server field
	ErrValidationNoClientOrServer = NewEntryValidationError("entry has neither client or server field")
	// ErrValidationWrongSequence occurs in case when sequence field of new entry is not sequence of old entry + 1
	ErrValidationWrongSequence = NewEntryValidationError("sequence field of new entry is not sequence of old entry + 1")
	// ErrValidationWrongTime occurs in case when previous entry timestamp is not set before current entry timestamp
	ErrValidationWrongTime = NewEntryValidationError("advertised entry timestamp is not greater than previous")
	// ErrValidationOutdatedTime occurs when the timestamp provided is not recent enough.
	ErrValidationOutdatedTime = NewEntryValidationError("advertised entry has outdated timestamp")
	// ErrValidationServerAddress occurs in case when client want to advertise wrong Server address
	ErrValidationServerAddress = NewEntryValidationError("advertising localhost listening address is not allowed in production mode")
	// ErrValidationEmptyServerAddress occurs when a server entry is submitted with an empty address.
	ErrValidationEmptyServerAddress = NewEntryValidationError("server address cannot be empty")

	errReverseMap = map[string]error{
		ErrKeyNotFound.Error():                  ErrKeyNotFound,
		ErrNoAvailableServers.Error():           ErrNoAvailableServers,
		ErrUnexpected.Error():                   ErrUnexpected,
		ErrUnauthorized.Error():                 ErrUnauthorized,
		ErrBadInput.Error():                     ErrBadInput,
		ErrValidationNonZeroSequence.Error():    ErrValidationNonZeroSequence,
		ErrValidationNilEphemerals.Error():      ErrValidationNilEphemerals,
		ErrValidationNilKeys.Error():            ErrValidationNilKeys,
		ErrValidationNonNilEphemerals.Error():   ErrValidationNonNilEphemerals,
		ErrValidationNoSignature.Error():        ErrValidationNoSignature,
		ErrValidationNoVersion.Error():          ErrValidationNoVersion,
		ErrValidationNoClientOrServer.Error():   ErrValidationNoClientOrServer,
		ErrValidationWrongSequence.Error():      ErrValidationWrongSequence,
		ErrValidationWrongTime.Error():          ErrValidationWrongTime,
		ErrValidationOutdatedTime.Error():       ErrValidationOutdatedTime,
		ErrValidationServerAddress.Error():      ErrValidationServerAddress,
		ErrValidationEmptyServerAddress.Error(): ErrValidationEmptyServerAddress,
	}
)

func errFromString(s string) error {
	err, ok := errReverseMap[s]
	if !ok {
		return ErrUnexpected
	}
	return err
}

// EntryValidationError represents transient error caused by invalid
// data in Entry
type EntryValidationError struct {
	Cause string
}

// NewEntryValidationError constructs a new validation error.
func NewEntryValidationError(cause string) error {
	return EntryValidationError{cause}
}

func (e EntryValidationError) Error() string {
	return fmt.Sprintf("entry validation error: %s", e.Cause)
}

// Entry represents a Dmsg Node's entry in the Discovery database.
type Entry struct {
	// The data structure's version.
	Version string `json:"version"`

	// An Entry of a given public key may need to iterate. This is the iteration sequence.
	Sequence uint64 `json:"sequence"`

	// Timestamp of the current iteration.
	Timestamp int64 `json:"timestamp"`

	// Static public key of an instance.
	Static cipher.PubKey `json:"static"`

	// Contains the instance's client meta if it's to be advertised as a DMSG Client.
	Client *Client `json:"client,omitempty"`

	// Contains the instance's server meta if it's to be advertised as a DMSG Server.
	Server *Server `json:"server,omitempty"`

	// Signature for proving authenticity of an Entry.
	Signature string `json:"signature,omitempty"`
}

func (e *Entry) String() string {
	res := ""
	res += fmt.Sprintf("\tversion: %s\n", e.Version)
	res += fmt.Sprintf("\tsequence: %d\n", e.Sequence)
	res += fmt.Sprintf("\tregistered at: %d\n", e.Timestamp)
	res += fmt.Sprintf("\tstatic public key: %s\n", e.Static)
	res += fmt.Sprintf("\tsignature: %s\n", e.Signature)

	if e.Client != nil {
		indentedStr := strings.Replace(e.Client.String(), "\n\t", "\n\t\t\t", -1)
		res += fmt.Sprintf("\tentry is registered as client. Related info: \n\t\t%s\n", indentedStr)
	}

	if e.Server != nil {
		indentedStr := strings.Replace(e.Server.String(), "\n\t", "\n\t\t", -1)
		res += fmt.Sprintf("\tentry is registered as server. Related info: \n\t%s\n", indentedStr)
	}

	return res
}

// Client contains parameters for Client instances.
type Client struct {
	// DelegatedServers contains a list of delegated servers represented by their public keys.
	DelegatedServers []cipher.PubKey `json:"delegated_servers"`
}

// String implements stringer
func (c *Client) String() string {
	res := "delegated servers: \n"

	for _, ds := range c.DelegatedServers {
		res += fmt.Sprintf("\t%s\n", ds)
	}

	return res
}

// Server contains parameters for Server instances.
type Server struct {
	// IPv4 or IPv6 public address of the DMSG Server.
	Address string `json:"address"`

	// AvailableSessions is the number of available sessions that the server can currently accept.
	AvailableSessions int `json:"availableSessions"`
}

// String implements stringer
func (s *Server) String() string {
	res := fmt.Sprintf("\taddress: %s\n", s.Address)
	res += fmt.Sprintf("\tavailable sessions: %d\n", s.AvailableSessions)

	return res
}

// NewClientEntry is a convenience function that returns a valid client entry, but this entry
// should be signed with the private key before sending it to the server
func NewClientEntry(pubkey cipher.PubKey, sequence uint64, delegatedServers []cipher.PubKey) *Entry {
	return &Entry{
		Version:   currentVersion,
		Sequence:  sequence,
		Client:    &Client{delegatedServers},
		Static:    pubkey,
		Timestamp: time.Now().UnixNano(),
	}
}

// NewServerEntry constructs a new Server entry.
func NewServerEntry(pk cipher.PubKey, seq uint64, addr string, availableSessions int) *Entry {
	return &Entry{
		Version:   currentVersion,
		Sequence:  seq,
		Server:    &Server{Address: addr, AvailableSessions: availableSessions},
		Static:    pk,
		Timestamp: time.Now().UnixNano(),
	}
}

// VerifySignature check if signature matches to Entry's PubKey.
func (e *Entry) VerifySignature() error {
	entry := *e

	// Get and parse signature
	signature := cipher.Sig{}
	err := signature.UnmarshalText([]byte(e.Signature))
	if err != nil {
		return err
	}

	// Set signature field to zero-value
	entry.Signature = ""

	// Get hash of the entry
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	return cipher.VerifyPubKeySignedPayload(e.Static, signature, entryJSON)
}

// Sign signs Entry with provided SecKey.
func (e *Entry) Sign(sk cipher.SecKey) error {
	// Clear previous signature, in case there was any
	e.Signature = ""

	entryJSON, err := json.Marshal(e)
	if err != nil {
		return err
	}

	sig, err := cipher.SignPayload(entryJSON, sk)
	if err != nil {
		return err
	}
	e.Signature = sig.Hex()
	return nil
}

// Validate checks if entry is valid.
func (e *Entry) Validate() error {
	// Must have version
	if e.Version == "" {
		return ErrValidationNoVersion
	}

	// Must be signed
	if e.Signature == "" {
		return ErrValidationNoSignature
	}

	// The Keys field must exist
	if e.Static.Null() {
		return ErrValidationNilKeys
	}

	// A record must have either client or server record
	if e.Client == nil && e.Server == nil {
		return ErrValidationNoClientOrServer
	}

	if e.Server != nil && e.Server.Address == "" {
		return ErrValidationEmptyServerAddress
	}

	now, ts := time.Now(), time.Unix(0, e.Timestamp)
	earliestAcceptable := now.Add(-entryLifetime)
	latestAcceptable := now.Add(allowedEntryTimestampError) // in case when time on nodes mismatches a bit

	if ts.After(latestAcceptable) || ts.Before(earliestAcceptable) {
		log.Warnf("Entry timestamp %v is not correct (now: %v)", ts, now)
		return ErrValidationOutdatedTime
	}

	return nil
}

// ValidateIteration verifies Entry's Sequence against nextEntry.
func (e *Entry) ValidateIteration(nextEntry *Entry) error {

	// Sequence value must be greater then current sequence.
	if nextEntry.Sequence <= e.Sequence {
		return ErrValidationWrongSequence
	}

	currentTimeStamp := time.Unix(0, e.Timestamp)
	nextTimeStamp := time.Unix(0, nextEntry.Timestamp)

	// Timestamp must be greater than current timestamp.
	if nextTimeStamp.Before(currentTimeStamp) {
		return ErrValidationWrongTime
	}

	return nil
}

// Copy performs a deep copy of two entries. It is safe to use with empty entries
func Copy(dst, src *Entry) {
	if dst.Server == nil && src.Server != nil {
		dst.Server = &Server{}
	}
	if dst.Client == nil && src.Client != nil {
		dst.Client = &Client{}
	}

	if src.Server == nil {
		dst.Server = nil
	} else {
		*dst.Server = *src.Server
	}
	if src.Client == nil {
		dst.Client = nil
	} else {
		*dst.Client = *src.Client
	}

	dst.Static = src.Static
	dst.Signature = src.Signature
	dst.Version = src.Version
	dst.Sequence = src.Sequence
	dst.Timestamp = src.Timestamp
}
