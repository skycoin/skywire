package commands

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/skychat/privacy"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var privacyStore *privacy.Store

func registerPrivacyHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/privacy", requireAuthFunc(privacyHandler))
	mux.HandleFunc("/privacy/block", requireAuthFunc(privacyBlockHandler))
	mux.HandleFunc("/privacy/accept", requireAuthFunc(privacyAcceptHandler))
}

func privacyStorePath() string {
	workDir := ""
	if appCl != nil {
		workDir = appCl.Config().ProcWorkDir
	}
	if workDir == "" {
		workDir = skyenv.LocalPath
	}
	return filepath.Join(workDir, "skychat-privacy.json")
}

func openPrivacyStore(path string) {
	store, err := privacy.OpenStore(path)
	if err != nil {
		appLog("Privacy: block list unavailable (%v), every peer can message", err)
		return
	}
	privacyStore = store
}

// inboundVerdict decides a direct message, a file or a pair invite from pk. A
// named contact needs no acceptance: naming someone says you know them.
func inboundVerdict(pk string) privacy.Verdict {
	return privacyStore.Verdict(pk, contactStore != nil && contactStore.Name(pk) != "")
}

// pendingRequests lists the peers in the history whose messages wait for the
// user to accept or block them.
func pendingRequests() []string {
	out := []string{}
	if !privacyStore.Approval() || historyStore == nil {
		return out
	}
	peers, err := historyStore.Peers()
	if err != nil {
		return out
	}
	for _, pk := range peers {
		if inboundVerdict(pk) == privacy.Request {
			out = append(out, pk)
		}
	}
	return out
}

type privacyState struct {
	Approval bool     `json:"approval"`
	Blocked  []string `json:"blocked"`
	Requests []string `json:"requests"`
}

func currentPrivacy() privacyState {
	return privacyState{
		Approval: privacyStore.Approval(),
		Blocked:  privacyStore.BlockedList(),
		Requests: pendingRequests(),
	}
}

// privacyHandler: GET the state, POST {"approval": bool} to turn approval on
// or off. Turning it on lets in everyone already in the history.
func privacyHandler(w http.ResponseWriter, r *http.Request) {
	if privacyStore == nil {
		http.Error(w, "privacy settings unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, currentPrivacy())
	case http.MethodPost:
		var body struct {
			Approval bool `json:"approval"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var existing []string
		if historyStore != nil {
			existing, _ = historyStore.Peers() //nolint:errcheck // none is a fine seed
		}
		if err := privacyStore.SetApproval(body.Approval, existing); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, currentPrivacy())
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// privacyBlockHandler: POST {"pk": "...", "blocked": bool}.
func privacyBlockHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PK      string `json:"pk"`
		Blocked bool   `json:"blocked"`
	}
	if !decodePrivacyPost(w, r, &body) {
		return
	}
	if err := privacyStore.SetBlocked(body.PK, body.Blocked); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, currentPrivacy())
}

// privacyAcceptHandler: POST {"pk": "..."} lets a requesting peer in.
func privacyAcceptHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PK string `json:"pk"`
	}
	if !decodePrivacyPost(w, r, &body) {
		return
	}
	if err := privacyStore.Accept(body.PK); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, currentPrivacy())
}

func decodePrivacyPost(w http.ResponseWriter, r *http.Request, body any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	if privacyStore == nil {
		http.Error(w, "privacy settings unavailable", http.StatusServiceUnavailable)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}
