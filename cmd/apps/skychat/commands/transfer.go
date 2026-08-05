// Package commands cmd/apps/skychat/commands/transfer.go c4-app-chat
//
// Moving a chat between devices, by hand.
//
//	GET  /export         the archive: address book + message history
//	GET  /export/<name>  the same, with a filename in the path (see below)
//	POST /import         merge an archive into this visor
//
// Nothing about skychat syncs. Two visors are two identities with two stores,
// and a message that reached one was never addressed to the other — so a
// desktop's history simply does not exist on a phone. Rather than pretend
// otherwise, this is the manual path: one JSON file out of the old device and
// into the new one.
//
// What travels is what is DATA: the messages this visor has kept and the
// names the operator gave to keys. What does not travel is everything that is
// an IDENTITY or a key — the visor's own keypair, group membership and its
// key material, pairing ratchets, undelivered transfers. Importing a group's
// messages puts the conversation on the new device to read; it does not make
// that device a member, which only rejoining does.
package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/history"
)

// archiveFormat/archiveVersion tag the file so an import can refuse
// something that is not one of ours instead of silently reading zero
// messages out of an unrelated JSON document.
const (
	archiveFormat  = "skychat-archive"
	archiveVersion = 1

	// importMaxBytes bounds a POSTed archive. Well past a real history
	// (the store's own default total cap is 10 MB) and far short of
	// anything that would exhaust memory decoding it.
	importMaxBytes = 64 << 20
)

// archive is the on-disk shape of an export. Every section is optional:
// a visor with persistence off still has an address book worth carrying,
// and one that was never given a nickname still has its messages.
type archive struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	// Visor is the public key of the visor this came from. Informational —
	// it is what tells the operator which device a file on a memory stick
	// came off, months later.
	Visor string `json:"visor,omitempty"`

	Contacts      map[string]string      `json:"contacts,omitempty"`
	Messages      []history.Message      `json:"messages,omitempty"`
	GroupMessages []history.GroupMessage `json:"group_messages,omitempty"`
}

// importReport is what /import answers with: every number the operator needs
// to decide whether the old device can be wiped.
type importReport struct {
	Contacts int `json:"contacts"`
	history.ImportResult
	// Persistence is false when this visor keeps no history at all, in
	// which case the messages in the archive had nowhere to go and the
	// counts above are zero for a reason worth naming.
	Persistence bool `json:"persistence"`
}

func registerTransferHandlers(mux *http.ServeMux) {
	// "/export/" is a prefix pattern serving the same handler: the path
	// suffix is ignored and exists only to name the downloaded file. A
	// client that derives the name from the URL rather than from
	// Content-Disposition — Android's WebView, which sees the navigation
	// but not the response — would otherwise save the archive as
	// "export.bin", which the import picker then filters out as not JSON.
	mux.HandleFunc("/export/", requireAuthFunc(exportHandler))
	mux.HandleFunc("/export", requireAuthFunc(exportHandler))
	mux.HandleFunc("/import", requireAuthFunc(importHandler))
}

// exportArchiveName is the filename an export is offered under, and the one
// the UI puts in the URL.
func exportArchiveName(now time.Time) string {
	return "skychat-" + now.UTC().Format("2006-01-02") + ".json"
}

func exportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	now := time.Now().UTC()
	out := archive{
		Format:     archiveFormat,
		Version:    archiveVersion,
		ExportedAt: now,
		Contacts:   contactNames(),
	}
	if appCl != nil {
		out.Visor = appCl.Config().VisorPK.Hex()
	}

	if historyStore != nil {
		msgs, groups, err := collectHistory()
		if err != nil {
			http.Error(w, "reading history: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out.Messages = msgs
		out.GroupMessages = groups
	}

	name := exportArchiveName(now)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	// The archive is a snapshot of a live store; a cached copy served to a
	// later export would be worse than useless.
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		// The response is already streaming, so this can only be logged.
		appLog("Export: writing archive failed: %v", err)
		return
	}
	appLog("Export: %d message(s), %d group message(s), %d contact(s)",
		len(out.Messages), len(out.GroupMessages), len(out.Contacts))
}

// collectHistory reads the whole store — every peer and every group, with no
// limit. That is the point of an export, and the store's own caps already
// bound how much there can be.
func collectHistory() ([]history.Message, []history.GroupMessage, error) {
	peers, err := historyStore.Peers()
	if err != nil {
		return nil, nil, fmt.Errorf("peers: %w", err)
	}
	var msgs []history.Message
	for _, peer := range peers {
		batch, err := historyStore.ListByPeer(peer, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("peer %s: %w", shortHexPK(peer), err)
		}
		msgs = append(msgs, batch...)
	}

	groups, err := historyStore.Groups()
	if err != nil {
		return nil, nil, fmt.Errorf("groups: %w", err)
	}
	var groupMsgs []history.GroupMessage
	for _, id := range groups {
		batch, err := historyStore.ListByGroup(id, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("group %s: %w", id, err)
		}
		groupMsgs = append(groupMsgs, batch...)
	}

	return msgs, groupMsgs, nil
}

func importHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes)
	var in archive
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "not a readable archive: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Format) != archiveFormat {
		http.Error(w, "not a SkyChat archive", http.StatusBadRequest)
		return
	}
	if in.Version > archiveVersion {
		http.Error(w, fmt.Sprintf(
			"archive is version %d; this SkyChat reads up to %d — update it first",
			in.Version, archiveVersion), http.StatusBadRequest)
		return
	}

	report := importReport{Persistence: historyStore != nil}

	// The address book first and separately: it is the half that works even
	// with persistence off, and Merge only ever fills gaps, so a name given
	// on this device is never overwritten by the archive's.
	if len(in.Contacts) > 0 && contactStore != nil {
		added, err := contactStore.Merge(in.Contacts)
		if err != nil {
			http.Error(w, "importing contacts: "+err.Error(), http.StatusInternalServerError)
			return
		}
		report.Contacts = added
	}

	if historyStore != nil {
		res, err := historyStore.Import(in.Messages, in.GroupMessages)
		// A full store still reports what it managed — the counts are the
		// answer, and the error only names why the rest did not fit.
		report.ImportResult = res
		if err != nil {
			appLog("Import: %v (%d contact(s) still imported)", err, report.Contacts)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInsufficientStorage)
			_ = json.NewEncoder(w).Encode(struct { //nolint:errcheck
				importReport
				Error string `json:"error"`
			}{report, err.Error()})
			return
		}
	}

	appLog("Import: %d message(s), %d group message(s), %d contact(s), %d duplicate(s) skipped",
		report.Messages, report.GroupMessages, report.Contacts, report.Duplicates)
	writeJSON(w, report)
}
