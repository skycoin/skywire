// Package setup setup transports
package setup

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// API contains Transport API methods
type API struct {
	tpM    *transport.Manager
	log    *logging.Logger
	router router.Router
}

// NewAPI creates a new Transport API
func NewAPI(tpM *transport.Manager, log *logging.Logger, router router.Router) *API {
	tpSetup := &API{
		tpM:    tpM,
		log:    log,
		router: router,
	}
	return tpSetup
}

// AddTransport specified by request
func (a *API) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*TransportSummary, error) {
	ctx := context.Background()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*20)
		defer cancel()
	}

	a.log.Debugf("Saving transport to %v via %v", remote, tpType)

	tp, err := a.tpM.SaveTransport(ctx, remote, network.Type(tpType), transport.LabelSkycoin)
	if err != nil {
		a.log.WithError(err).Error("Cannot save transport")
		return nil, err
	}

	a.log.Debugf("Saved transport to %v via %v, label %s", remote, tpType, tp.Entry.Label)
	return newTransportSummary(a.tpM, tp, false, a.router.SetupIsTrusted(tp.Remote())), nil
}

// ErrIncorrectType is returned when transport was not created by transport setup
var ErrIncorrectType = errors.New("transport was not created by skycoin")

// RemoveTransport removes all transports that match given request
func (a *API) RemoveTransport(tid uuid.UUID) error {
	a.log.WithField("ID", tid).Debug("Removing transport")
	tr, err := a.tpM.GetTransportByID(tid)
	if err != nil {
		return err
	}
	if tr.Entry.Label != transport.LabelSkycoin {
		return ErrIncorrectType
	}
	a.tpM.DeleteTransport(tid)
	return nil
}

// GetTransports returns all transports of this node that have been established by transport setup system
func (a *API) GetTransports(_ struct{}) ([]*TransportSummary, error) {
	tps := a.tpM.GetTransportsByLabel(transport.LabelSkycoin)
	var res []*TransportSummary
	for _, tp := range tps {
		tSum := newTransportSummary(a.tpM, tp, false, a.router.SetupIsTrusted(tp.Remote()))
		res = append(res, tSum)
	}
	return res, nil
}

// TransportSummary summarizes a Transport.
type TransportSummary struct {
	ID      uuid.UUID           `json:"id"`
	Local   cipher.PubKey       `json:"local_pk"`
	Remote  cipher.PubKey       `json:"remote_pk"`
	Type    network.Type        `json:"type"`
	Log     *transport.LogEntry `json:"log,omitempty"`
	IsSetup bool                `json:"is_setup"`
	Label   transport.Label     `json:"label"`
}

func newTransportSummary(tm *transport.Manager, tp *transport.ManagedTransport, includeLogs, isSetup bool) *TransportSummary {
	summary := &TransportSummary{
		ID:      tp.Entry.ID,
		Local:   tm.Local(),
		Remote:  tp.Remote(),
		Type:    tp.Type(),
		IsSetup: isSetup,
		Label:   tp.Entry.Label,
	}
	if includeLogs {
		summary.Log = tp.LogEntry
	}
	return summary
}
