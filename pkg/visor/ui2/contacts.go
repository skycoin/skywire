// Package ui2 pkg/visor/ui2/contacts.go c3-vis-core
package ui2

import (
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
)

// List is the contact list view.
type List struct {
	PK     string
	All    []contacts.Contact
	Filter string
	Err    string
}

// Shown is the contacts whose name, key, mail address or group contains the
// filter, as the Angular tab matched them.
func (l List) Shown() []contacts.Contact {
	f := strings.ToLower(strings.TrimSpace(l.Filter))
	if f == "" {
		return l.All
	}
	var out []contacts.Contact
	for _, c := range l.All {
		fields := append([]string{c.Name}, c.Mail...)
		fields = append(fields, c.Groups...)
		for _, k := range c.Keys {
			fields = append(fields, k.Hex())
		}
		for _, s := range fields {
			if strings.Contains(strings.ToLower(s), f) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Form is the edit form, with list fields as one entry per line.
type Form struct {
	PK       string
	UID      string
	Name     string
	Keys     string
	Mail     string
	Payments string
	Groups   string
	Notes    string
	Err      string
}

// FormOf fills the form from a contact.
func FormOf(pk string, c contacts.Contact) Form {
	keys := make([]string, len(c.Keys))
	for i, k := range c.Keys {
		keys[i] = k.Hex()
	}
	pays := make([]string, len(c.Payments))
	for i, p := range c.Payments {
		pays[i] = p.URI()
	}
	return Form{
		PK:       pk,
		UID:      c.UID,
		Name:     c.Name,
		Keys:     strings.Join(keys, "\n"),
		Mail:     strings.Join(c.Mail, "\n"),
		Payments: strings.Join(pays, "\n"),
		Groups:   strings.Join(c.Groups, ", "),
		Notes:    c.Notes,
	}
}

// Contact parses the form back into a contact.
func (f Form) Contact() (contacts.Contact, error) {
	c := contacts.Contact{
		UID:   f.UID,
		Name:  strings.TrimSpace(f.Name),
		Mail:  lines(f.Mail),
		Notes: strings.TrimSpace(f.Notes),
	}
	for _, l := range lines(f.Keys) {
		var pk cipher.PubKey
		if err := pk.Set(l); err != nil {
			return c, fmt.Errorf("key %q: %w", l, err)
		}
		c.Keys = append(c.Keys, pk)
	}
	for _, l := range lines(f.Payments) {
		p, err := contacts.ParsePayment(l)
		if err != nil {
			return c, err
		}
		c.Payments = append(c.Payments, p)
	}
	for g := range strings.SplitSeq(f.Groups, ",") {
		if g = strings.TrimSpace(g); g != "" {
			c.Groups = append(c.Groups, g)
		}
	}
	return c, nil
}

func lines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (l List) url(rest string) string { return contactsURL(l.PK, rest) }
func (f Form) url(rest string) string { return contactsURL(f.PK, rest) }

func contactsURL(pk, rest string) string {
	return "visors/" + pk + "/contacts" + rest
}
