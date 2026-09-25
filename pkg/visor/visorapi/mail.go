// Package visorapi pkg/visor/visorapi/mail.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
)

// Mail is the visor's own mailbox (pkg/skymail): mail to its PK over
// skynet and dmsg, kept verbatim in a Maildir.
type Mail interface {
	// MailStatus reports whether the mailbox runs, its addresses, and
	// who may deliver to it.
	MailStatus() (*MailStatus, error)
	// MailList lists a folder ("INBOX" or "Sent"), newest first.
	MailList(folder string) ([]skymail.Summary, error)
	// MailRead renders one message and marks it read.
	MailRead(folder, id string) (*skymail.Rendered, error)
	// MailRaw returns one message exactly as stored.
	MailRaw(folder, id string) ([]byte, error)
	// MailAttachment decodes attachment n of a message, numbered as
	// MailRead lists them.
	MailAttachment(folder, id string, n int) (*skymail.AttachmentData, error)
	// MailSend delivers a message now; a recipient whose visor cannot
	// be reached gets an error in the result, not a queue entry.
	MailSend(msg skymail.Outgoing) (*skymail.SendResult, error)
	// MailDelete removes one message.
	MailDelete(folder, id string) error
	// MailSetWhitelist replaces the PKs allowed to deliver; empty
	// accepts everyone. Persisted with the mail.
	MailSetWhitelist(pks []cipher.PubKey) error
	// MailSetSettings changes whether the mailbox runs and its limits,
	// at once, and keeps them beside the mail (settings.json).
	MailSetSettings(u MailSettingsUpdate) error
}

// MailStatus is the state of the visor's mailbox.
type MailStatus struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Reason  string `json:"reason,omitempty"` // why it is not running
	// Limits are the effective ones; Usage is the bytes held now.
	Limits skymail.Limits `json:"limits"`
	Usage  int64          `json:"usage"`
	// Address and AddressDmsg are the default addresses; any local part
	// at the same domain reaches the same mailbox.
	Address     string          `json:"address,omitempty"`
	AddressDmsg string          `json:"address_dmsg,omitempty"`
	Dir         string          `json:"dir,omitempty"`
	Whitelist   []cipher.PubKey `json:"whitelist"`
	Unread      int             `json:"unread"`
	Total       int             `json:"total"`
}

// MailMessageRequest names one message.
type MailMessageRequest struct {
	Folder string
	ID     string
}

// MailAttachmentRequest names one attachment of one message.
type MailAttachmentRequest struct {
	Folder string
	ID     string
	N      int
}

// MailSettingsUpdate changes the fields that are set; nil leaves one as
// it is. A zero limit restores its default, a negative one removes it.
type MailSettingsUpdate struct {
	Enable         *bool          `json:"enable,omitempty"`
	MaxMessageSize *int64         `json:"max_message_size,omitempty"`
	MaxTotalSize   *int64         `json:"max_total_size,omitempty"`
	MaxAge         *time.Duration `json:"max_age,omitempty"`
}

// MailSettingsWire is MailSettingsUpdate as it crosses the RPC. gob
// flattens pointers and drops zero values, so a pointer to false or to
// 0 would arrive as nil: "leave it" instead of "turn it off" or "back
// to the default". The Set flags carry the difference.
type MailSettingsWire struct {
	EnableSet, MaxMessageSizeSet, MaxTotalSizeSet, MaxAgeSet bool

	Enable         bool
	MaxMessageSize int64
	MaxTotalSize   int64
	MaxAge         time.Duration
}

// Wire encodes u for the RPC.
func (u MailSettingsUpdate) Wire() MailSettingsWire {
	var w MailSettingsWire
	if u.Enable != nil {
		w.EnableSet, w.Enable = true, *u.Enable
	}
	if u.MaxMessageSize != nil {
		w.MaxMessageSizeSet, w.MaxMessageSize = true, *u.MaxMessageSize
	}
	if u.MaxTotalSize != nil {
		w.MaxTotalSizeSet, w.MaxTotalSize = true, *u.MaxTotalSize
	}
	if u.MaxAge != nil {
		w.MaxAgeSet, w.MaxAge = true, *u.MaxAge
	}
	return w
}

// Update decodes w.
func (w MailSettingsWire) Update() MailSettingsUpdate {
	var u MailSettingsUpdate
	if w.EnableSet {
		u.Enable = &w.Enable
	}
	if w.MaxMessageSizeSet {
		u.MaxMessageSize = &w.MaxMessageSize
	}
	if w.MaxTotalSizeSet {
		u.MaxTotalSize = &w.MaxTotalSize
	}
	if w.MaxAgeSet {
		u.MaxAge = &w.MaxAge
	}
	return u
}
