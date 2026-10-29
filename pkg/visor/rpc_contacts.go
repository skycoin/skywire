// Package visor pkg/visor/rpc_contacts.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// ContactsList lists the address book. See Visor.ContactsList.
func (r *RPC) ContactsList(_ *struct{}, out *[]contacts.Contact) (err error) {
	defer rpcutil.LogCall(r.log, "ContactsList", nil)(nil, &err)
	*out, err = r.visor.ContactsList()
	return err
}

// ContactsPut adds or replaces a contact. See Visor.ContactsPut.
func (r *RPC) ContactsPut(in *contacts.Contact, out *contacts.Contact) (err error) {
	defer rpcutil.LogCall(r.log, "ContactsPut", nil)(nil, &err)
	c, err := r.visor.ContactsPut(*in)
	if err != nil {
		return err
	}
	*out = *c
	return nil
}

// ContactsDelete removes a contact. See Visor.ContactsDelete.
func (r *RPC) ContactsDelete(uid *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ContactsDelete", uid)(nil, &err)
	return r.visor.ContactsDelete(*uid)
}

// ContactsImport reads a .vcf file into the book. See Visor.ContactsImport.
func (r *RPC) ContactsImport(vcf *[]byte, n *int) (err error) {
	defer rpcutil.LogCall(r.log, "ContactsImport", nil)(n, &err)
	*n, err = r.visor.ContactsImport(*vcf)
	return err
}

// ContactsExport returns the book as a .vcf file. See Visor.ContactsExport.
func (r *RPC) ContactsExport(_ *struct{}, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "ContactsExport", nil)(nil, &err)
	*out, err = r.visor.ContactsExport()
	return err
}
