// Package contacts pkg/contacts/keys.go c3-vis-core
package contacts

import (
	"errors"
	"os"
	"slices"

	"github.com/skycoin/skywire/pkg/cipher"
)

// KeySave adds a contact named name holding pk, unless a contact already holds
// it. An existing contact keeps its name, so an app saving a key cannot rename
// someone the operator named here. added reports whether the book changed.
func (s *Store) KeySave(pk cipher.PubKey, name string) (added bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.listLocked()
	if err != nil {
		return false, err
	}
	for _, c := range list {
		if c.OwnsKey(pk) {
			return false, nil
		}
	}
	c, err := Contact{Name: name, Keys: []cipher.PubKey{pk}}.normalized()
	if err != nil {
		return false, err
	}
	return true, s.writeLocked(c)
}

// KeyForget takes pk, and every mail address mailOfKey reports as pk's, off
// every contact. A contact left with no key, mail or payment address is
// deleted. mailOfKey may be nil.
func (s *Store) KeyForget(pk cipher.PubKey, mailOfKey func(addr string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.listLocked()
	if err != nil {
		return err
	}
	for _, c := range list {
		keys := slices.DeleteFunc(slices.Clone(c.Keys), func(k cipher.PubKey) bool { return k == pk })
		mail := slices.DeleteFunc(slices.Clone(c.Mail), func(m string) bool { return mailOfKey != nil && mailOfKey(m) })
		if len(keys) == len(c.Keys) && len(mail) == len(c.Mail) {
			continue
		}
		if len(keys) == 0 && len(mail) == 0 && len(c.Payments) == 0 {
			if err := os.Remove(s.path(c.UID)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		c.Keys, c.Mail = keys, mail
		if err := s.writeLocked(c); err != nil {
			return err
		}
	}
	return nil
}
