// Package visor pkg/visor/hypervisor_handlers_contacts.go c3-vis-core
package visor

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/httputil"
)

// The hypervisor's view of a visor's contact book, for the dashboard's
// contacts tab. Every route calls the visor's contacts API, the same one
// `skywire cli contacts` uses, so it works for remote visors too.

// maxContactsImport bounds a .vcf upload. A few thousand cards fit easily.
const maxContactsImport = 4 << 20

// contactsRoutes registers the contact book routes.
func (hv *Hypervisor) contactsRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/contacts", hv.getContacts())
	r.Post("/visors/{pk}/contacts", hv.postContact())
	r.Get("/visors/{pk}/contacts/export", hv.getContactsExport())
	r.Post("/visors/{pk}/contacts/import", hv.postContactsImport())
	r.Delete("/visors/{pk}/contacts/{uid}", hv.deleteContact())
}

func (hv *Hypervisor) getContacts() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		list, err := ctx.API.ContactsList()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		if list == nil {
			list = []contacts.Contact{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, list)
	})
}

// postContact adds a contact, or replaces the one with the same UID.
func (hv *Hypervisor) postContact() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var c contacts.Contact
		if err := httputil.ReadJSON(r, &c); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("malformed contact: %w", err))
			return
		}
		saved, err := ctx.API.ContactsPut(c)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, saved)
	})
}

func (hv *Hypervisor) deleteContact() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		err := ctx.API.ContactsDelete(r.PathValue("uid"))
		switch {
		case errors.Is(err, contacts.ErrNotFound):
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
		case err != nil:
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
		default:
			httputil.WriteJSON(w, r, http.StatusOK, true)
		}
	})
}

func (hv *Hypervisor) getContactsExport() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		vcf, err := ctx.API.ContactsExport()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="contacts.vcf"`)
		_, _ = w.Write(vcf) //nolint:errcheck
	})
}

// postContactsImport takes a .vcf body and answers how many cards it added or
// replaced. A file with one bad card imports nothing.
func (hv *Hypervisor) postContactsImport() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		vcf, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxContactsImport))
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusRequestEntityTooLarge, err)
			return
		}
		n, err := ctx.API.ContactsImport(vcf)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]int{"imported": n})
	})
}
