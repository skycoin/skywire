// Package visorapi pkg/visor/visorapi/contacts.go c3-vis-core
package visorapi

import "github.com/skycoin/skywire/pkg/contacts"

// Contacts is the visor's address book (pkg/contacts), kept as vCard files.
type Contacts interface {
	// ContactsList returns every contact, sorted by name.
	ContactsList() ([]contacts.Contact, error)
	// ContactsPut adds a contact, or replaces the one with its UID, and
	// returns it as stored.
	ContactsPut(c contacts.Contact) (*contacts.Contact, error)
	// ContactsDelete removes the contact with uid.
	ContactsDelete(uid string) error
	// ContactsImport adds every card of a .vcf file, replacing contacts that
	// share a UID, and returns how many it read.
	ContactsImport(vcf []byte) (int, error)
	// ContactsExport returns the whole book as one .vcf file.
	ContactsExport() ([]byte, error)
}
