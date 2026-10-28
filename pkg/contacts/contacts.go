// Package contacts pkg/contacts/contacts.go c3-vis-core
//
// The visor's address book: the people its operator knows, with the skywire
// keys, mail addresses and payment addresses that reach them. One store serves
// the CLI, the hypervisor and the visor's apps, so a name set once shows
// everywhere.
//
// Each contact is a vCard 4.0 file (RFC 6350), so a book exports to and
// imports from any address book. vCard has no field for a skywire key or a
// coin address, so those are X- properties; a skywire key is also written as
// an IMPP skychat URI (RFC 4770), which most clients show as a chat handle.
//
// Local by design: nothing here is published or sent to a peer.
package contacts

import (
	"fmt"
	"sort"
	"strings"

	"github.com/emersion/go-vcard"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// vCard properties for what RFC 6350 has no field for.
const (
	fieldSkywireKey = "X-SKYWIRE-PK"
	fieldPayment    = "X-PAYMENT"
	imppScheme      = "skychat:"
)

// Payment is an address a contact can be paid at.
type Payment struct {
	// Coin is the address's URI scheme, e.g. "skycoin" or "bitcoin" (BIP 321).
	Coin    string `json:"coin"`
	Address string `json:"address"`
}

// URI is the payment as a URI, e.g. "skycoin:2jBbGxZ...".
func (p Payment) URI() string { return p.Coin + ":" + p.Address }

// ParsePayment reads a payment URI such as "bitcoin:bc1q...".
func ParsePayment(s string) (Payment, error) {
	coin, addr, ok := strings.Cut(strings.TrimSpace(s), ":")
	coin = strings.ToLower(coin)
	if !ok || coin == "" || addr == "" || strings.ContainsAny(coin, " /") {
		return Payment{}, fmt.Errorf("payment %q: want coin:address, e.g. skycoin:2jBbGx", s)
	}
	return Payment{Coin: coin, Address: addr}, nil
}

// Contact is one person or service in the book.
type Contact struct {
	UID      string          `json:"uid"`
	Name     string          `json:"name"`
	Keys     []cipher.PubKey `json:"keys,omitempty"`
	Mail     []string        `json:"mail,omitempty"`
	Payments []Payment       `json:"payments,omitempty"`
	Groups   []string        `json:"groups,omitempty"`
	Notes    string          `json:"notes,omitempty"`
}

// HasKey reports whether pk is one of the contact's keys.
func (c Contact) HasKey(pk cipher.PubKey) bool {
	for _, k := range c.Keys {
		if k == pk {
			return true
		}
	}
	return false
}

// card is the contact as a vCard 4.0.
func (c Contact) card() vcard.Card {
	card := vcard.Card{}
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldUID, "urn:uuid:"+c.UID)
	card.SetValue(vcard.FieldFormattedName, c.Name)
	for _, k := range c.Keys {
		card.AddValue(fieldSkywireKey, k.Hex())
		card.AddValue(vcard.FieldIMPP, imppScheme+k.Hex())
	}
	for _, m := range c.Mail {
		card.AddValue(vcard.FieldEmail, m)
	}
	for _, p := range c.Payments {
		card.AddValue(fieldPayment, p.URI())
	}
	for _, g := range c.Groups {
		card.AddValue(vcard.FieldCategories, g)
	}
	if c.Notes != "" {
		card.SetValue(vcard.FieldNote, c.Notes)
	}
	return card
}

// fromCard reads a contact from any vCard. Fields it does not know are
// dropped; a key it cannot parse is an error, since the book is keyed by them.
func fromCard(card vcard.Card) (Contact, error) {
	c := Contact{
		UID:   strings.TrimPrefix(card.Value(vcard.FieldUID), "urn:uuid:"),
		Name:  strings.TrimSpace(card.PreferredValue(vcard.FieldFormattedName)),
		Mail:  card.Values(vcard.FieldEmail),
		Notes: card.Value(vcard.FieldNote),
	}
	keys := card.Values(fieldSkywireKey)
	for _, v := range card.Values(vcard.FieldIMPP) {
		if s, ok := strings.CutPrefix(v, imppScheme); ok {
			keys = append(keys, s)
		}
	}
	for _, s := range keys {
		var pk cipher.PubKey
		if err := pk.Set(strings.TrimSpace(s)); err != nil {
			return Contact{}, fmt.Errorf("contact %q: skywire key %q: %w", c.Name, s, err)
		}
		if !c.HasKey(pk) {
			c.Keys = append(c.Keys, pk)
		}
	}
	for _, v := range card.Values(fieldPayment) {
		p, err := ParsePayment(v)
		if err != nil {
			return Contact{}, fmt.Errorf("contact %q: %w", c.Name, err)
		}
		c.Payments = append(c.Payments, p)
	}
	for _, v := range card.Values(vcard.FieldCategories) {
		for _, g := range strings.Split(v, ",") {
			if g = strings.TrimSpace(g); g != "" {
				c.Groups = append(c.Groups, g)
			}
		}
	}
	return c.normalized()
}

// normalized checks c and fills a missing UID. A UID that cannot name a file
// is replaced, since a vCard from elsewhere may carry any string.
func (c Contact) normalized() (Contact, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return Contact{}, fmt.Errorf("a contact needs a name")
	}
	if id, err := uuid.Parse(c.UID); err == nil {
		c.UID = id.String()
	} else {
		c.UID = uuid.NewString()
	}
	sort.Strings(c.Groups)
	return c, nil
}
