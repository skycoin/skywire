// Package visor pkg/visor/contacts.go c3-vis-core
package visor

import (
	"bytes"
	"path/filepath"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/skymailbridge"
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

// appContactNames is the book as key hex to name for the app named appName,
// or nil when that app may not read it or the book cannot be read.
func (v *Visor) appContactNames(appName string) map[string]string {
	if appName != skyenv.SkychatName {
		return nil
	}
	list, err := v.ContactsList()
	if err != nil {
		return nil
	}
	return contacts.Names(list)
}

// ChatContactSave lets skychat's address book add a key to the visor's, so a
// contact saved in either shows in both.
func (h chatHost) ChatContactSave(pk cipher.PubKey, name string) (bool, error) {
	s, err := h.v.contactStore()
	if err != nil {
		return false, err
	}
	return s.KeySave(pk, name)
}

// ChatContactForget takes a key skychat's address book removed off the
// visor's, with the mail addresses at that key.
func (h chatHost) ChatContactForget(pk cipher.PubKey) error {
	s, err := h.v.contactStore()
	if err != nil {
		return err
	}
	return s.KeyForget(pk, func(addr string) bool {
		for _, sfx := range skymail.Suffixes {
			if got, _, ok, err := skymailbridge.ParseRecipient(addr, sfx, "a"); err == nil && ok && got == pk {
				return true
			}
		}
		return false
	})
}
