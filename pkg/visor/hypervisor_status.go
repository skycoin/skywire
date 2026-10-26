package visor

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// hvStatus draws the status page of the hypervisor and every visor connected
// to it, one section each, from the samples each visor keeps.
type hvStatus struct {
	hv    *Hypervisor
	once  sync.Once
	board *charts.Board

	mu    sync.Mutex
	pages map[cipher.PubKey]*charts.Page
}

func (s *hvStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Visors answer over RPC, each up to its RPC timeout, past the server's 10s
	// WriteTimeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute)) //nolint:errcheck // the default deadline still applies
	s.once.Do(func() {
		s.pages = map[cipher.PubKey]*charts.Page{}
		s.board = &charts.Board{
			Title:    "Skywire visors",
			About:    s.hv.c.PK.Hex(),
			Sections: s.sections,
			Log:      s.hv.logger,
			Separate: true,
		}
	})
	s.board.ServeHTTP(w, r)
}

// sections is the hypervisor first, then its visors by key.
func (s *hvStatus) sections() []charts.Section {
	s.hv.mu.RLock()
	apis := map[cipher.PubKey]visorapi.API{}
	for pk, c := range s.hv.remoteVisors {
		apis[pk] = c.API
	}
	self := s.hv.selfConn.API
	s.hv.mu.RUnlock()
	keys := make([]cipher.PubKey, 0, len(apis))
	for pk := range apis {
		keys = append(keys, pk)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Hex() < keys[j].Hex() })
	if self != nil {
		keys = append([]cipher.PubKey{s.hv.c.PK}, keys...)
		apis[s.hv.c.PK] = self
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]charts.Section, 0, len(keys))
	for _, pk := range keys {
		p := s.pages[pk]
		if p == nil {
			st := &remoteCharts{}
			p = &charts.Page{
				Title: "Visor " + pk.Hex(),
				Stats: charts.NewStatsView("visor"),
				Store: st,
				Log:   s.hv.logger,
				Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
					return buildVisorCharts(ctx, st, r, now)
				},
			}
			s.pages[pk] = p
		}
		p.Store.(*remoteCharts).set(apis[pk])
		out = append(out, charts.Section{Page: p})
	}
	return out
}

// remoteCharts reads a visor's status samples over its API. It keeps the last
// window it read, since a page reads the same window for each of its parts.
type remoteCharts struct {
	mu   sync.Mutex
	api  visorapi.API
	args visorapi.VisorChartsArgs
	last *visorapi.VisorCharts
}

func (rc *remoteCharts) set(api visorapi.API) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.api != api {
		rc.api, rc.last = api, nil
	}
}

var errReadOnly = errors.New("a visor's samples are read through its API")

func (rc *remoteCharts) Add(context.Context, charts.Sample) error     { return errReadOnly }
func (rc *remoteCharts) AddStart(context.Context, charts.Start) error { return errReadOnly }

func (rc *remoteCharts) fetch(args visorapi.VisorChartsArgs) (*visorapi.VisorCharts, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.last != nil && rc.args == args {
		return rc.last, nil
	}
	if rc.api == nil {
		return nil, errors.New("visor not connected")
	}
	out, err := rc.api.VisorCharts(args)
	if err != nil {
		if strings.Contains(err.Error(), "can't find") {
			return nil, charts.NoteError("This visor's build keeps no status charts.")
		}
		return nil, err
	}
	rc.args, rc.last = args, out
	return out, nil
}

func (rc *remoteCharts) Range(_ context.Context, from, to time.Time, hourly bool) ([]charts.Sample, error) {
	vc, err := rc.fetch(visorapi.VisorChartsArgs{From: from, To: to, Hourly: hourly})
	if err != nil {
		return nil, err
	}
	out := make([]charts.Sample, 0, len(vc.Samples))
	for _, s := range vc.Samples {
		out = append(out, charts.Sample{At: s.At, V: s.V})
	}
	return out, nil
}

// Starts reads the starts of the last window, as every part of a page reads
// the same one before it asks for starts.
func (rc *remoteCharts) Starts(_ context.Context, from, to time.Time) ([]charts.Start, error) {
	rc.mu.Lock()
	vc := rc.last
	rc.mu.Unlock()
	if vc == nil {
		return nil, nil
	}
	var out []charts.Start
	for _, s := range vc.Starts {
		if !s.At.Before(from) && s.At.Before(to) {
			out = append(out, charts.Start{At: s.At, Version: s.Version, Commit: s.Commit})
		}
	}
	return out, nil
}
