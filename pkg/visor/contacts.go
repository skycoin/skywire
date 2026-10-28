// Package visor pkg/visor/contacts.go c3-vis-core
package visor

import (
	"bytes"
	"path/filepath"
	"sync"

	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// contactBook opens the address book on first use.
type contactBook struct {
	once  sync.Once
	store *contacts.Store
	err   error
}

// contactsDir is "contacts" beside the local path, as the mailbox is, so the
// wasm visor keeps the book across a reload.
func contactsDir(conf *visorconfig.V1) string {
	return filepath.Join(filepath.Dir(filepath.Clean(conf.LocalPath)), "contacts")
}

func (v *Visor) contactStore() (*contacts.Store, error) {
	b := &v.contacts
	b.once.Do(func() { b.store, b.err = contacts.OpenStore(contactsDir(v.conf)) })
	return b.store, b.err
}

// ContactsList implements visorapi.API.
func (v *Visor) ContactsList() ([]contacts.Contact, error) {
	s, err := v.contactStore()
	if err != nil {
		return nil, err
	}
	return s.List()
}

// ContactsPut implements visorapi.API.
func (v *Visor) ContactsPut(c contacts.Contact) (*contacts.Contact, error) {
	s, err := v.contactStore()
	if err != nil {
		return nil, err
	}
	c, err = s.Put(c)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ContactsDelete implements visorapi.API.
func (v *Visor) ContactsDelete(uid string) error {
	s, err := v.contactStore()
	if err != nil {
		return err
	}
	return s.Delete(uid)
}

// ContactsImport implements visorapi.API.
func (v *Visor) ContactsImport(vcf []byte) (int, error) {
	s, err := v.contactStore()
	if err != nil {
		return 0, err
	}
	return s.Import(bytes.NewReader(vcf))
}

// ContactsExport implements visorapi.API.
func (v *Visor) ContactsExport() ([]byte, error) {
	s, err := v.contactStore()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = s.Export(&buf)
	return buf.Bytes(), err
}
