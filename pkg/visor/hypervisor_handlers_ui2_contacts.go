// Package visor pkg/visor/hypervisor_handlers_ui2_contacts.go c3-vis-core
package visor

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The contacts tab of the server rendered UI.

func (hv *Hypervisor) ui2ContactsRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/contacts", hv.ui2Contacts(false))
	r.Get("/ui2/visors/{pk}/contacts/panel", hv.ui2Contacts(true))
	r.Get("/ui2/visors/{pk}/contacts/rows", hv.ui2ContactRows())
	r.Get("/ui2/visors/{pk}/contacts/new", hv.ui2ContactForm())
	r.Get("/ui2/visors/{pk}/contacts/{uid}/edit", hv.ui2ContactForm())
	r.Post("/ui2/visors/{pk}/contacts", hv.ui2ContactSave())
	r.Post("/ui2/visors/{pk}/contacts/import", hv.ui2ContactsImport())
	r.Delete("/ui2/visors/{pk}/contacts/{uid}", hv.ui2ContactDelete())
}

// ui2List loads the book into a list view. A load error is shown in the view.
func ui2List(ctx *httpCtx, pk, filter string) ui2.List {
	l := ui2.List{PK: pk, Filter: filter}
	all, err := ctx.API.ContactsList()
	if err != nil {
		l.Err = err.Error()
	}
	l.All = all
	return l
}

// ui2Contacts serves the whole page, or with panel set only the list panel.
func (hv *Hypervisor) ui2Contacts(panel bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		l := ui2List(ctx, pk, r.URL.Query().Get("q"))
		if panel {
			hv.render(w, r, ui2.ListView(l), ui2.Count(len(l.All), true))
			return
		}
		hv.render(w, r, ui2.ContactsPage(l))
	})
}

func (hv *Hypervisor) ui2ContactRows() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		hv.render(w, r, ui2.Rows(ui2List(ctx, r.PathValue("pk"), r.URL.Query().Get("q"))))
	})
}

// ui2ContactForm serves an empty form, or with {uid} the contact's form.
func (hv *Hypervisor) ui2ContactForm() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk, uid := r.PathValue("pk"), r.PathValue("uid")
		f := ui2.Form{PK: pk}
		if uid != "" {
			all, err := ctx.API.ContactsList()
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
			found := false
			for _, c := range all {
				if c.UID == uid {
					f, found = ui2.FormOf(pk, c), true
					break
				}
			}
			if !found {
				httputil.WriteJSON(w, r, http.StatusNotFound, contacts.ErrNotFound)
				return
			}
		}
		hv.render(w, r, ui2.FormView(f))
	})
}

// ui2ContactSave adds or replaces a contact. A bad form comes back with the
// error shown in it and the user's input kept.
func (hv *Hypervisor) ui2ContactSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		pk := r.PathValue("pk")
		f := ui2.Form{
			PK:       pk,
			UID:      r.PostForm.Get("uid"),
			Name:     r.PostForm.Get("name"),
			Keys:     r.PostForm.Get("keys"),
			Mail:     r.PostForm.Get("mail"),
			Payments: r.PostForm.Get("payments"),
			Groups:   r.PostForm.Get("groups"),
			Notes:    r.PostForm.Get("notes"),
		}
		c, err := f.Contact()
		if err == nil {
			_, err = ctx.API.ContactsPut(c)
		}
		if err != nil {
			f.Err = err.Error()
			hv.render(w, r, ui2.FormView(f))
			return
		}
		l := ui2List(ctx, pk, "")
		hv.render(w, r, ui2.ListView(l), ui2.Count(len(l.All), true), ui2.Toast(hv.ui2T(r, "contacts.saved"), false))
	})
}

func (hv *Hypervisor) ui2ContactDelete() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		err := ctx.API.ContactsDelete(r.PathValue("uid"))
		switch {
		case errors.Is(err, contacts.ErrNotFound):
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		case err != nil:
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		l := ui2List(ctx, r.PathValue("pk"), "")
		hv.render(w, r, ui2.ListView(l), ui2.Count(len(l.All), true), ui2.Toast(hv.ui2T(r, "contacts.deleted"), false))
	})
}

// ui2ContactsImport takes a multipart .vcf upload. A file with one bad card
// imports nothing, as with the JSON route.
func (hv *Hypervisor) ui2ContactsImport() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		r.Body = http.MaxBytesReader(w, r.Body, maxContactsImport)
		file, _, err := r.FormFile("vcf")
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		defer file.Close() //nolint:errcheck
		vcf, err := io.ReadAll(file)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusRequestEntityTooLarge, err)
			return
		}
		n, err := ctx.API.ContactsImport(vcf)
		l := ui2List(ctx, r.PathValue("pk"), "")
		toast := ui2.Toast(hv.ui2T(r, "contacts.imported", "n", strconv.Itoa(n)), false)
		if err != nil {
			toast = ui2.Toast(err.Error(), true)
		}
		hv.render(w, r, ui2.ListView(l), ui2.Count(len(l.All), true), toast)
	})
}
