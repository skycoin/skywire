// Package group pkg/skychat/group/parked.go c3-app-chat
package group

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ParkPort labels the join requests and decisions left on dmsg-async inboxes
// for a requester and admins who are not online at the same time.
const ParkPort = ProbePort

// Parker leaves payload on to's dmsg-async inbox. The visor seals it to to and
// signs it with its own key, so the receiving Manager learns who sent it.
type Parker func(to cipher.PubKey, payload []byte) error

// SetParker installs the Parker. Without one nothing parks.
func (m *Manager) SetParker(p Parker) {
	m.parkMu.Lock()
	m.park = p
	m.parkMu.Unlock()
}

func (m *Manager) parker() Parker {
	m.parkMu.RLock()
	defer m.parkMu.RUnlock()
	return m.park
}

// parkJoinRequest leaves req on the inbox of every target and reports how
// many took it.
func (m *Manager) parkJoinRequest(targets []cipher.PubKey, req JoinRequestMsg) int {
	p := m.parker()
	if p == nil {
		return 0
	}
	body, err := encodeJoinRequest(req)
	if err != nil {
		return 0
	}
	n := 0
	for _, pk := range targets {
		if err := p(pk, body); err != nil {
			m.log.WithError(err).WithField("id", req.GroupID).WithField("admin", pk.Hex()).
				Debug("group: join: parking the request failed")
			continue
		}
		n++
	}
	return n
}

// parkDecision tells requester pk what the group decided. It runs in the
// background because a deposit can take seconds and an admin's command should
// not wait on it.
func (m *Manager) parkDecision(pk cipher.PubKey, resp JoinResponseMsg) {
	p := m.parker()
	if p == nil || !resp.Status.Terminal() {
		return
	}
	body, err := encodeJoinResponse(resp)
	if err != nil {
		return
	}
	go func() {
		if err := p(pk, body); err != nil {
			m.log.WithError(err).WithField("id", resp.GroupID).WithField("peer", pk.Hex()).
				Warn("group: join: parking the decision failed")
		}
	}()
}

// HandleParked takes one frame fetched from this visor's inbox, sealed by
// from and taken by the host at. A nil error removes it from the host, so
// frames that cannot apply return nil too.
func (m *Manager) HandleParked(from cipher.PubKey, payload []byte, at time.Time) error {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return nil
	}
	switch head.Kind {
	case frameKindJoinRequest:
		msg, err := decodeJoinRequest(payload)
		if err != nil || (msg.RequesterPK != (cipher.PubKey{}) && msg.RequesterPK != from) {
			return nil
		}
		resp := m.admissionHandler(msg.GroupID)(JoinRequest{
			GroupID:  msg.GroupID,
			PK:       from,
			Note:     msg.Note,
			PoW:      msg.PoW,
			AskAgain: msg.AskAgain,
			AskedAt:  at.UTC(),
			Status:   JoinStatusPending,
		})
		resp.GroupID = msg.GroupID
		m.parkDecision(from, resp)
	case frameKindJoinResponse:
		resp, err := decodeJoinResponse(payload)
		if err != nil {
			return nil
		}
		r, ok, err := m.store.Get(resp.GroupID)
		if err != nil {
			return err
		}
		// An admin may also approve a request it declined before.
		waiting := r.Status == StatusAwaitingApproval ||
			(r.Status == StatusDenied && resp.Status == JoinStatusAdmitted)
		if !ok || !waiting || !containsPK(r.AdmissionTargets(m.myPK), from) {
			return nil
		}
		m.applyJoinResponse(r, resp)
	}
	return nil
}

// joinUnreachable reports whether err from SendJoinRequestAny means no admin
// could be reached, as opposed to an answer.
func joinUnreachable(err error) bool {
	return err != nil && !errors.Is(err, ErrJoinNoResponse) && !errors.Is(err, ErrJoinNoAdmin) &&
		!errIsTerminalJoin(err)
}
