// Package visorapi pkg/visor/visorapi/dmsg_async.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
)

// DmsgAsync is store and forward for offline keys (pkg/dmsg/dmsgasync).
type DmsgAsync interface {
	// DmsgAsyncStatus reports this visor's host and inbox.
	DmsgAsyncStatus() (*DmsgAsyncStatus, error)
	// DmsgAsyncSend delivers a payload to an offline key by depositing it on
	// the key's inbox hosts.
	DmsgAsyncSend(req DmsgAsyncSendRequest) (*DmsgAsyncSendResult, error)
	// DmsgAsyncTake fetches and acks what waits for this visor on ports that
	// no in-visor handler serves.
	DmsgAsyncTake(ports []uint16) ([]dmsgasync.Message, error)
	// DmsgAsyncSync re-registers with the inbox hosts, republishes the record
	// and fetches now.
	DmsgAsyncSync() error
}

// DmsgAsyncStatus is DmsgAsyncStatus's answer.
type DmsgAsyncStatus struct {
	Host  *DmsgAsyncHostStatus `json:"host,omitempty"`
	Inbox DmsgAsyncInboxStatus `json:"inbox"`
}

// DmsgAsyncHostStatus describes this visor as an inbox host.
type DmsgAsyncHostStatus struct {
	Store      string           `json:"store"`
	Limits     dmsgasync.Limits `json:"limits"`
	Stats      dmsgasync.Stats  `json:"stats"`
	Registered []cipher.PubKey  `json:"registered"`
}

// DmsgAsyncInboxStatus describes where this visor's own messages wait.
type DmsgAsyncInboxStatus struct {
	Enabled    bool                 `json:"enabled"`
	Record     *dmsgasync.Record    `json:"record,omitempty"`
	Hosts      []DmsgAsyncInboxHost `json:"hosts,omitempty"`
	Published  bool                 `json:"published"`
	PublishErr string               `json:"publish_error,omitempty"`
	Handlers   []uint16             `json:"handlers,omitempty"`
	LastFetch  time.Time            `json:"last_fetch,omitzero"`
	Fetched    int                  `json:"fetched"`
}

// DmsgAsyncInboxHost is the state of one inbox host.
type DmsgAsyncInboxHost struct {
	PK         cipher.PubKey `json:"pk"`
	Registered time.Time     `json:"registered,omitzero"`
	Error      string        `json:"error,omitempty"`
}

// DmsgAsyncSendRequest is DmsgAsyncSend's argument.
type DmsgAsyncSendRequest struct {
	To      cipher.PubKey `json:"to"`
	Port    uint16        `json:"port"`
	Payload []byte        `json:"payload"`
	// Ref makes retries of one message idempotent. Empty picks a new one.
	Ref string `json:"ref,omitempty"`
}

// DmsgAsyncSendResult is DmsgAsyncSend's answer.
type DmsgAsyncSendResult struct {
	Host cipher.PubKey `json:"host"`
	Ref  string        `json:"ref"`
}
